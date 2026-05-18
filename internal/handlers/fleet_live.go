package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// FleetLive exposes an SSE fan-out of vehicle hot-state (lat/lng/status from
// the vehicles table, synced from the latest telemetry ping). Used by the
// Next.js map shell so fleet markers move without polling /api/vehicles.
type FleetLive struct {
	Repo *store.Repository
}

func (h *FleetLive) Register(rg *gin.RouterGroup) {
	rg.GET("/vehicles/live/stream", auth.RequireAnyPerm("view_vehicle", "view_telemetry"), h.stream)
}

type fleetVehicleSnap struct {
	ID       string  `json:"id"`
	Plate    string  `json:"plate"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Status   string  `json:"status"`
	Heading  float64 `json:"heading"`
	Location string  `json:"location"`
}

type fleetPayload struct {
	GeneratedAt string             `json:"generatedAt"`
	Vehicles    []fleetVehicleSnap `json:"vehicles"`
}

func (h *FleetLive) stream(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	ctx := c.Request.Context()
	emit := func(vs []models.Vehicle) {
		snaps := make([]fleetVehicleSnap, 0, len(vs))
		for _, v := range vs {
			snaps = append(snaps, fleetVehicleSnap{
				ID: v.ID, Plate: v.Plate, Lat: v.Lat, Lng: v.Lng,
				Status: v.Status, Heading: v.Heading, Location: v.Location,
			})
		}
		b, err := json.Marshal(fleetPayload{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			Vehicles:    snaps,
		})
		if err != nil {
			return
		}
		fmt.Fprintf(c.Writer, "event: fleet\ndata: %s\n\n", b)
		flusher.Flush()
	}

	if vs, err := h.Repo.Vehicles.List(ctx); err == nil {
		emit(vs)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			vs, err := h.Repo.Vehicles.List(ctx)
			if err != nil {
				continue
			}
			emit(vs)
			fmt.Fprintf(c.Writer, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}
