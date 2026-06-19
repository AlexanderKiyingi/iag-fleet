package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// FleetLive exposes an SSE fan-out of vehicle hot-state (lat/lng/status from
// the vehicles table, synced from the latest telemetry ping). Used by the
// Next.js map shell so fleet markers move without polling /api/vehicles.
type FleetLive struct {
	Repo *store.Repository
	Hub  *iot.Hub
	Gate *StreamGate // optional: caps concurrent SSE streams; nil = unlimited
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
	release, ok := h.Gate.reserveStream(c)
	if !ok {
		return
	}
	defer release()

	expiry := tokenExpiry(c)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	flusher, isFlusher := c.Writer.(http.Flusher)
	if !isFlusher {
		return
	}

	ctx := c.Request.Context()
	snapsByID := make(map[string]fleetVehicleSnap)

	emitAll := func() {
		snaps := make([]fleetVehicleSnap, 0, len(snapsByID))
		for _, s := range snapsByID {
			snaps = append(snaps, s)
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

	loadAll := func() error {
		vs, err := h.Repo.Vehicles.List(ctx)
		if err != nil {
			return err
		}
		for _, v := range vs {
			snapsByID[v.ID] = vehicleSnap(v)
		}
		emitAll()
		return nil
	}

	_ = loadAll() // initial paint; subsequent failures surface on the ticker
	var pollFails int

	var liveCh <-chan iot.Ping
	var liveCancel func()
	if h.Hub != nil {
		liveCh, liveCancel = h.Hub.SubscribeLive()
		defer liveCancel()
	}

	// Fallback poll between sparse events or when ingest runs in another process without Redis.
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-liveCh:
			if !ok {
				liveCh = nil
				continue
			}
			if p.VehicleID == "" {
				continue
			}
			if v, err := h.Repo.Vehicles.Get(ctx, p.VehicleID); err == nil {
				snapsByID[v.ID] = vehicleSnap(v)
				emitAll()
			}
		case <-tick.C:
			// Close once the bearer token lapses so a stream can't outlive its auth.
			if tokenExpired(expiry, time.Now()) {
				sseEvent(c.Writer, flusher, "expired", `{"reason":"token expired; reconnect"}`)
				return
			}
			if err := loadAll(); err != nil {
				// Surface and close after a run of failures rather than streaming
				// keep-alives over a dead DB connection forever.
				pollFails++
				if pollFails >= maxConsecutivePollFails {
					sseEvent(c.Writer, flusher, "error", `{"reason":"fleet state temporarily unavailable"}`)
					return
				}
			} else {
				pollFails = 0
			}
			fmt.Fprintf(c.Writer, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

func vehicleSnap(v models.Vehicle) fleetVehicleSnap {
	return fleetVehicleSnap{
		ID: v.ID, Plate: v.Plate, Lat: v.Lat, Lng: v.Lng,
		Status: v.Status, Heading: v.Heading, Location: v.Location,
	}
}
