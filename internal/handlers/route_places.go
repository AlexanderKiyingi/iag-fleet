package handlers

import (
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// RoutePlaces serves the JMP waypoint library (migration 0060).
//
// Not geofences: these pins are researched online or dropped on the map and
// reused on journey plans. Access rides JMP permissions so a dispatcher who
// can author a plan can also grow the library without a separate IAM grant.
type RoutePlaces struct {
	Repo *store.Repository
}

func (h *RoutePlaces) Register(rg *gin.RouterGroup) {
	g := rg.Group("/route-places")
	view := auth.RequireAnyPerm("view_jmp", "view_route_place")
	write := auth.RequireAnyPerm("add_jmp", "change_jmp", "add_route_place", "change_route_place")
	del := auth.RequireAnyPerm("delete_jmp", "delete_route_place")
	g.GET("", view, h.list)
	g.POST("", write, h.upsert)
	g.DELETE("/:id", del, h.remove)
}

type routePlaceBody struct {
	Name          string   `json:"name"`
	Lat           *float64 `json:"lat"`
	Lng           *float64 `json:"lng"`
	Source        string   `json:"source"`
	OsmID         string   `json:"osmId"`
	GooglePlaceID string   `json:"googlePlaceId"`
	DisplayName   string   `json:"displayName"`
	CountryCode   string   `json:"countryCode"`
	Category      string   `json:"category"`
	Status        string   `json:"status"`
	Notes         string   `json:"notes"`
}

func (h *RoutePlaces) list(c *gin.Context) {
	items, err := h.Repo.RoutePlaces.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	q := strings.ToLower(strings.TrimSpace(c.Query("q")))
	cat := strings.ToLower(strings.TrimSpace(c.Query("category")))
	cc := strings.ToUpper(strings.TrimSpace(c.Query("country")))
	out := make([]models.RoutePlace, 0, len(items))
	for _, p := range items {
		if p.Status == "inactive" && c.Query("all") != "1" && c.Query("all") != "true" {
			continue
		}
		if cat != "" && !strings.EqualFold(p.Category, cat) {
			continue
		}
		if cc != "" && !strings.EqualFold(p.CountryCode, cc) {
			continue
		}
		if q != "" {
			hay := strings.ToLower(p.Name + " " + p.DisplayName + " " + p.Category + " " + p.OsmID + " " + p.GooglePlaceID)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, p)
	}
	c.JSON(http.StatusOK, gin.H{"places": out, "count": len(out)})
}

func (h *RoutePlaces) upsert(c *gin.Context) {
	var body routePlaceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if body.Lat == nil || body.Lng == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lat and lng are required"})
		return
	}
	lat, lng := *body.Lat, *body.Lng
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 || math.IsNaN(lat) || math.IsNaN(lng) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lat/lng out of range"})
		return
	}
	source := strings.TrimSpace(body.Source)
	if source == "" {
		source = "manual"
	}
	switch source {
	case "manual", "map", "nominatim", "google":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "source must be manual, map, nominatim, or google"})
		return
	}
	osmID := strings.TrimSpace(body.OsmID)
	googleID := strings.TrimSpace(body.GooglePlaceID)
	category := strings.TrimSpace(body.Category)
	if category == "" {
		category = "place"
	}
	status := strings.TrimSpace(body.Status)
	if status == "" {
		status = "active"
	}

	ctx := c.Request.Context()
	existing, err := h.Repo.RoutePlaces.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Prefer google_place_id, then osm_id / seed key, else same name within ~50 m.
	var hit *models.RoutePlace
	for i := range existing {
		p := &existing[i]
		if googleID != "" && p.GooglePlaceID == googleID {
			hit = p
			break
		}
	}
	if hit == nil {
		for i := range existing {
			p := &existing[i]
			if osmID != "" && p.OsmID == osmID {
				hit = p
				break
			}
		}
	}
	if hit == nil && googleID == "" && osmID == "" {
		for i := range existing {
			p := &existing[i]
			if !strings.EqualFold(strings.TrimSpace(p.Name), name) {
				continue
			}
			if haversineM(p.Lat, p.Lng, lat, lng) <= 50 {
				hit = p
				break
			}
		}
	}

	if hit != nil {
		updated, err := h.Repo.RoutePlaces.Update(ctx, hit.ID, func(p *models.RoutePlace) {
			p.Name = name
			p.Lat = lat
			p.Lng = lng
			p.Source = source
			if osmID != "" {
				p.OsmID = osmID
			}
			if googleID != "" {
				p.GooglePlaceID = googleID
			}
			if category != "" {
				p.Category = category
			}
			if dn := strings.TrimSpace(body.DisplayName); dn != "" {
				p.DisplayName = dn
			}
			if cc := strings.TrimSpace(body.CountryCode); cc != "" {
				p.CountryCode = cc
			}
			p.Status = status
			if notes := strings.TrimSpace(body.Notes); notes != "" {
				p.Notes = notes
			}
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"place": updated, "created": false})
		return
	}

	item := models.RoutePlace{
		ID:            uuid.NewString(),
		Name:          name,
		Lat:           lat,
		Lng:           lng,
		Source:        source,
		OsmID:         osmID,
		GooglePlaceID: googleID,
		DisplayName:   strings.TrimSpace(body.DisplayName),
		CountryCode:   strings.TrimSpace(body.CountryCode),
		Category:      category,
		Status:        status,
		Notes:         strings.TrimSpace(body.Notes),
	}
	created, err := h.Repo.RoutePlaces.Add(ctx, item)
	if err != nil {
		if msg, ok := badRequestFromPg(err); ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"place": created, "created": true})
}

func (h *RoutePlaces) remove(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	if err := h.Repo.RoutePlaces.Delete(c.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "place not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func haversineM(lat1, lng1, lat2, lng2 float64) float64 {
	const earth = 6371000.0
	toRad := math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLng := (lng2 - lng1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earth * math.Asin(math.Min(1, math.Sqrt(a)))
}
