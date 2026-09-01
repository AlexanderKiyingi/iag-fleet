package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/auth"
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
	// Snap is the process-wide snapshot every connection reads from. Required:
	// without it each connection would go back to running its own poll, which
	// is the thing this field exists to prevent.
	Snap *FleetSnapshotter
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
	// Speed and LastSeen are what a map draws next to the marker: "42 km/h,
	// 3 min ago". They were absent from this payload, so a client driven by
	// this stream could move a dot but had to fall back to polling
	// /api/vehicles for the two fields that say whether the dot is current.
	// A marker that moves while its timestamp is stale reads as a bug, so the
	// stream now carries everything the marker needs.
	Speed         float64 `json:"speed"`
	LastSeen      string  `json:"lastSeen,omitempty"`
	LastFixSource string  `json:"lastFixSource,omitempty"`
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

	// Everything below reads the process-wide snapshot. This connection issues
	// no query of its own — not on connect, not on a timer, not on a ping.
	changed, unsubscribe := h.Snap.Subscribe()
	defer unsubscribe()

	var sentVersion uint64
	emitIfNew := func() {
		payload, version, _ := h.Snap.Current()
		if version == sentVersion || len(payload) == 0 {
			return
		}
		// payload is immutable once published, so writing it without holding a
		// lock is safe and a slow client cannot stall the refresh loop.
		fmt.Fprintf(c.Writer, "event: fleet\ndata: %s\n\n", payload)
		flusher.Flush()
		sentVersion = version
	}

	emitIfNew() // initial paint, straight from memory

	// The keep-alive tick is now only a keep-alive: it proves the connection is
	// alive to any proxy in between and re-checks the token. Data arrives on
	// `changed` instead of being polled for.
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
			// A refresh failure also signals, so check health before writing.
			if _, _, fails := h.Snap.Current(); fails >= maxConsecutivePollFails {
				sseEvent(c.Writer, flusher, "error", `{"reason":"fleet state temporarily unavailable"}`)
				return
			}
			emitIfNew()
		case <-tick.C:
			// Close once the bearer token lapses so a stream can't outlive its auth.
			if tokenExpired(expiry, time.Now()) {
				sseEvent(c.Writer, flusher, "expired", `{"reason":"token expired; reconnect"}`)
				return
			}
			if _, _, fails := h.Snap.Current(); fails >= maxConsecutivePollFails {
				sseEvent(c.Writer, flusher, "error", `{"reason":"fleet state temporarily unavailable"}`)
				return
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
		Speed: v.Speed, LastSeen: v.LastSeen, LastFixSource: v.LastFixSource,
	}
}
