package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/routing"
)

// Routing exposes road routing (OSRM proxy) plus straight-line fallback.
type Routing struct {
	// OSRMBaseURL is e.g. https://router.project-osrm.org or your self-hosted engine (no path suffix).
	OSRMBaseURL string
}

func (h *Routing) Register(rg *gin.RouterGroup) {
	// Same audience as fleet map / telemetry — operators planning legs.
	r := rg.Group("/routing", auth.RequireAnyPerm("view_vehicle", "view_telemetry"))
	r.GET("/capabilities", h.capabilities)
	r.POST("/plan", h.plan)
}

// routePoint uses pointers so a missing or null lat/lng is detectable (reject {}, {"lat":1}, etc.).
type routePoint struct {
	Lat *float64 `json:"lat"`
	Lng *float64 `json:"lng"`
}

type planRequestBody struct {
	Profile string       `json:"profile"` // driving | walking | cycling — forwarded to OSRM
	Points  []routePoint `json:"points"`  // at least 2, max 25
}

type planResponse struct {
	Mode      string      `json:"mode"` // osrm | straight_line
	Profile   string      `json:"profile"`
	DistanceM float64     `json:"distanceM"`
	DurationS *float64    `json:"durationS,omitempty"`
	// Coordinates in GeoJSON order (longitude, latitude) for each vertex.
	Coordinates [][2]float64 `json:"coordinates"`
	Warnings    []string     `json:"warnings,omitempty"`
}

const maxWaypoints = 25

var allowedProfiles = map[string]string{
	"driving": "driving",
	"walking": "walking",
	"cycling": "cycling",
}

func (h *Routing) capabilities(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"osrmConfigured": strings.TrimSpace(h.OSRMBaseURL) != "",
		"profiles":       []string{"driving", "walking", "cycling"},
		"fallback":       "straight_line",
		"maxWaypoints":   maxWaypoints,
	})
}

func (h *Routing) plan(c *gin.Context) {
	var body planRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(body.Points) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "need at least two points"})
		return
	}
	if len(body.Points) > maxWaypoints {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too many waypoints"})
		return
	}

	profile := strings.TrimSpace(strings.ToLower(body.Profile))
	if profile == "" {
		profile = "driving"
	}
	osrmProfile, ok := allowedProfiles[profile]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown profile — use driving, walking, or cycling"})
		return
	}

	latlng := make([][2]float64, len(body.Points))
	for i, p := range body.Points {
		if p.Lat == nil || p.Lng == nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("points[%d] must include both lat and lng (reject empty objects or nulls)", i),
			})
			return
		}
		lat, lng := *p.Lat, *p.Lng
		if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("points[%d] coordinates out of range", i)})
			return
		}
		latlng[i] = [2]float64{lat, lng}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 22*time.Second)
	defer cancel()

	base := strings.TrimSpace(h.OSRMBaseURL)
	if base != "" {
		rr, err := routing.RequestOSRM(ctx, base, osrmProfile, latlng)
		if err == nil && rr != nil {
			dur := rr.DurationS
			c.JSON(http.StatusOK, planResponse{
				Mode:        "osrm",
				Profile:     profile,
				DistanceM:   rr.DistanceM,
				DurationS:   &dur,
				Coordinates: rr.Coordinates,
			})
			return
		}
		if err != nil {
			slog.Warn("routing OSRM failed, using straight-line fallback",
				"err", err,
				"profile", osrmProfile,
				"waypoints", len(latlng),
			)
		}
	}

	coords, dist := routing.StraightLinePath(latlng)
	var warns []string
	if base != "" {
		warns = append(warns, "OSRM request failed or returned no route — using straight-line fallback")
	} else {
		warns = append(warns, "ROUTING_OSRM_URL is not set — using straight-line (great-circle) geometry only")
	}
	c.JSON(http.StatusOK, planResponse{
		Mode:        "straight_line",
		Profile:     profile,
		DistanceM:   dist,
		Coordinates: coords,
		Warnings:    warns,
	})
}
