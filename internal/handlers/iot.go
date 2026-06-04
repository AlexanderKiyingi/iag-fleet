package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// IoT routes:
//
//	Device admin (perm: manage_iot_device)
//	  GET    /api/iot/devices
//	  POST   /api/iot/devices               { serial, label?, vehicleId?, issueKey? }
//	  GET    /api/iot/devices/:id
//	  PATCH  /api/iot/devices/:id           { label?, vehicleId?, isActive? }
//	  DELETE /api/iot/devices/:id
//	  POST   /api/iot/devices/:id/rotate-key
//
//	HTTP ingestion runs on Fleet_IoT (see GET /api/iot/ingestion).
//
//	Replay / live (perm: view_telemetry on GPS/track endpoints)
//	  GET    /api/vehicles/:id/track?from=&to=&limit=&after=
//	  GET    /api/vehicles/:id/track/latest
//	  GET    /api/vehicles/:id/track/stream    (SSE; emits whenever the latest ping changes)
//
//	Fuel derived from telemetry (history, events, summary, fleet anomalies):
//	  view_telemetry OR view_fuel_record — matches operators who manage
//	  manual fuel data but still need IoT-driven charts on vehicle pages.
type IoT struct {
	Store  *iot.Store
	Hub *iot.Hub
	Repo   *store.Repository // optional: vehicle validation + list enrichment; nil in tests
}

// requireStore ensures telemetry tables are wired; tests may build a
// router without a pool-backed store — respond consistently instead of
// panicking or registering no routes.
func (h *IoT) requireStore(c *gin.Context) {
	if h.Store == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "telemetry store not configured"})
		return
	}
	c.Next()
}

func (h *IoT) Register(rg *gin.RouterGroup) {
	fuelRead := auth.RequireAnyPerm("view_telemetry", "view_fuel_record")

	dev := rg.Group("/iot/devices", auth.RequirePerm("manage_iot_device"), h.requireStore)
	dev.GET("", h.listDevices)
	dev.POST("", h.createDevice)
	dev.GET("/:id", h.getDevice)
	dev.PATCH("/:id", h.updateDevice)
	dev.DELETE("/:id", h.deleteDevice)
	dev.POST("/:id/rotate-key", h.rotateKey)
	dev.POST("/:id/test-ping", h.testPing)

	// Operator-facing ingestion contract (URLs + limits + sample JSON) for relays and scripts.
	rg.GET("/iot/ingestion", auth.RequirePerm("manage_iot_device"), h.requireStore, h.ingestionGuide)

	rg.GET("/vehicles/:id/track", auth.RequirePerm("view_telemetry"), h.requireStore, h.track)
	rg.GET("/vehicles/:id/track/latest", auth.RequirePerm("view_telemetry"), h.requireStore, h.latest)
	rg.GET("/vehicles/:id/track/stream", auth.RequirePerm("view_telemetry"), h.requireStore, h.stream)
	rg.GET("/vehicles/:id/telemetry/daily", auth.RequirePerm("view_telemetry"), h.requireStore, h.telemetryDaily)
	rg.POST("/vehicles/:id/trips/detect", auth.RequirePerm("change_trip"), h.requireStore, h.detectTrips)

	rg.GET("/vehicles/:id/fuel/history", fuelRead, h.requireStore, h.fuelHistory)
	rg.GET("/vehicles/:id/fuel/events", fuelRead, h.requireStore, h.fuelEvents)
	rg.GET("/vehicles/:id/fuel/summary", fuelRead, h.requireStore, h.fuelSummary)
	rg.GET("/fuel/anomalies", fuelRead, h.requireStore, h.fleetFuelAnomalies)
}

// ─────────────────────────── Device admin ──────────────────────────

type createDeviceBody struct {
	Serial    string `json:"serial" binding:"required"`
	Label     string `json:"label"`
	VehicleID string `json:"vehicleId"`
	IssueKey  bool   `json:"issueKey"`
}

func (h *IoT) createDevice(c *gin.Context) {
	var body createDeviceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.VehicleID != "" {
		if err := h.ensureVehicle(c.Request.Context(), body.VehicleID); err != nil {
			respondVehicleOr500(c, err)
			return
		}
	}

	created, err := h.Store.CreateDevice(c.Request.Context(), iot.CreateDeviceInput{
		Serial:    body.Serial,
		Label:     body.Label,
		VehicleID: body.VehicleID,
		IssueKey:  body.IssueKey,
	})
	if err != nil {
		if strings.Contains(err.Error(), "23505") { // unique_violation
			c.JSON(http.StatusConflict, gin.H{"error": "serial already registered"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// `apiKey` is the only time the plaintext is ever exposed.
	c.JSON(http.StatusCreated, created)
}

// listDeviceItem is the wire shape for GET /api/iot/devices (includes optional plate).
type listDeviceItem struct {
	iot.Device
	VehiclePlate string `json:"vehiclePlate,omitempty"`
}

func (h *IoT) listDevices(c *gin.Context) {
	ctx := c.Request.Context()
	ds, err := h.Store.ListDevices(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	plateBy := map[string]string{}
	if h.Repo != nil {
		vs, err := h.Repo.Vehicles.List(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, v := range vs {
			plateBy[v.ID] = v.Plate
		}
	}

	out := make([]listDeviceItem, 0, len(ds))
	for _, d := range ds {
		out = append(out, listDeviceItem{Device: d, VehiclePlate: plateBy[d.VehicleID]})
	}
	c.JSON(http.StatusOK, out)
}

func (h *IoT) getDevice(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	d, err := h.Store.GetDevice(c.Request.Context(), id)
	if err != nil {
		respondIotError(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

type updateDeviceBody struct {
	Label     *string `json:"label"`
	VehicleID *string `json:"vehicleId"`
	IsActive  *bool   `json:"isActive"`
}

func (h *IoT) updateDevice(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body updateDeviceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.VehicleID != nil {
		if *body.VehicleID != "" {
			if err := h.ensureVehicle(c.Request.Context(), *body.VehicleID); err != nil {
				respondVehicleOr500(c, err)
				return
			}
		}
	}

	d, err := h.Store.UpdateDevice(c.Request.Context(), id, iot.UpdateDeviceInput{
		Label:     body.Label,
		VehicleID: body.VehicleID,
		IsActive:  body.IsActive,
	})
	if err != nil {
		respondIotError(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

func (h *IoT) deleteDevice(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.Store.DeleteDevice(c.Request.Context(), id); err != nil {
		respondIotError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *IoT) rotateKey(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	plaintext, err := h.Store.RotateAPIKey(c.Request.Context(), id)
	if err != nil {
		respondIotError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"apiKey": plaintext})
}

// ingestionGuide documents how HTTP relays authenticate and POST pings (operator-only).
func (h *IoT) ingestionGuide(c *gin.Context) {
	ingestBase := strings.TrimRight(os.Getenv("TELEMETRY_INGEST_URL"), "/")
	if ingestBase == "" {
		ingestBase = "http://fleet-iot-ingest:4080"
	}
	c.JSON(http.StatusOK, gin.H{
		"service": "Fleet_IoT",
		"http": gin.H{
			"method":      "POST",
			"url":         ingestBase + "/v1/pings",
			"legacyPath":  ingestBase + "/api/iot/pings",
			"contentType": "application/json",
			"authorization": gin.H{
				"scheme": "Bearer",
				"hint":   "Plaintext API key issued on device create or rotate-key; only the SHA-256 digest is stored.",
			},
			"maxBatch": iot.MaxIngestBatch,
			"bodyShape": gin.H{
				"single":         "object or array of objects",
				"vehicleId":      "optional when the device row has a default vehicle binding (required otherwise)",
				"requiredFields": []string{"lat", "lng"},
				"optionalFields": []string{"ts", "altitude", "heading", "speedKmh", "satellites", "odo", "fuelLevel", "ignition", "eventId", "raw"},
			},
			"sample": []map[string]any{{
				"lat": 0.3476, "lng": 32.5825,
				"speedKmh": 0, "fuelLevel": 62,
				"raw": map[string]any{"note": "optional opaque JSON from device"},
			}},
		},
		"tcp": gin.H{
			"binary":     "Teltonika Codec 8 / 8E",
			"listener":   "Fleet_IoT TCP gateway (default :5027)",
			"identifier": "IMEI must match iot_devices.serial; no bearer token on wire.",
		},
	})
}

// testPing inserts one synthetic ping for onboarding verification (uses device → vehicle binding).
func (h *IoT) testPing(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	ctx := c.Request.Context()
	d, err := h.Store.GetDevice(ctx, id)
	if err != nil {
		respondIotError(c, err)
		return
	}
	if d.VehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "assign this device to a vehicle before sending a test ping",
		})
		return
	}
	if err := h.ensureVehicle(ctx, d.VehicleID); err != nil {
		respondVehicleOr500(c, err)
		return
	}
	if !d.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "device is disabled — enable it before testing"})
		return
	}

	devID := d.ID
	speed := 0.0
	raw := json.RawMessage(`{"source":"test-ping"}`)
	p := iot.Ping{
		VehicleID: d.VehicleID,
		DeviceID:  &devID,
		TS:        time.Now().UTC(),
		Lat:       0.3476,
		Lng:       32.5825,
		SpeedKmh:  &speed,
		Raw:       raw,
	}
	if _, err := h.Store.InsertPings(ctx, []iot.Ping{p}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.Store.SyncVehicleFromPing(ctx, p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = h.Store.MarkSeen(ctx, devID, c.ClientIP())
	if h.Hub != nil {
		h.Hub.Publish(p)
	}
	_ = h.Store.ApplyGeofenceTransitions(ctx, iot.ProcessGeofences(p))
	latest, err := h.Store.LatestPing(ctx, d.VehicleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"vehicleId": d.VehicleID,
		"ping":      latest,
	})
}

func (h *IoT) ensureVehicle(ctx context.Context, vehicleID string) error {
	if vehicleID == "" || h.Repo == nil {
		return nil
	}
	_, err := h.Repo.Vehicles.Get(ctx, vehicleID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errUnknownVehicle
		}
		return err
	}
	return nil
}

var errUnknownVehicle = errors.New("unknown vehicle")

func respondVehicleOr500(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errUnknownVehicle):
		c.JSON(http.StatusNotFound, gin.H{"error": "vehicle not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// ─────────────────────────── Replay / live ──────────────────────────

func (h *IoT) requireVehicleForTrack(c *gin.Context, vehicleID string) bool {
	if err := h.ensureVehicle(c.Request.Context(), vehicleID); err != nil {
		respondVehicleOr500(c, err)
		return false
	}
	return true
}

func (h *IoT) latest(c *gin.Context) {
	id := c.Param("id")
	if !h.requireVehicleForTrack(c, id) {
		return
	}
	p, err := h.Store.LatestPing(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no telemetry"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Fuel history / events / summary + fleet-wide anomalies align with the
// HAULA UI (30-day presets on fuel + vehicle telemetry tabs).
const maxFuelTelemetryRange = 31 * 24 * time.Hour

func (h *IoT) track(c *gin.Context) {
	id := c.Param("id")
	if !h.requireVehicleForTrack(c, id) {
		return
	}
	from, to, err := parseTrackRange(c, iot.MaxTrackReplayRange())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	after, err := parseTrackAfter(c.Query("after"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	pings, err := h.Store.Track(c.Request.Context(), id, from, to, limit, after)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := gin.H{
		"vehicleId": id,
		"from":      from,
		"to":        to,
		"pings":     pings,
		"count":     len(pings),
	}
	if len(pings) > 0 {
		maxRows := iot.MaxTrackRowLimit()
		effectiveLimit := limit
		if effectiveLimit <= 0 {
			effectiveLimit = 5000
		}
		if effectiveLimit > maxRows {
			effectiveLimit = maxRows
		}
		if len(pings) >= effectiveLimit {
			last := pings[len(pings)-1].TS
			resp["nextAfter"] = last
			resp["hasMore"] = true
		}
	}
	c.JSON(http.StatusOK, resp)
}

const ssePollInterval = 2 * time.Second

// maxDailyTelemetryRange allows reading rolled-up history beyond raw ping retention.
const maxDailyTelemetryRange = 365 * 24 * time.Hour

func (h *IoT) telemetryDaily(c *gin.Context) {
	id := c.Param("id")
	if !h.requireVehicleForTrack(c, id) {
		return
	}
	from, to, err := parseTrackRange(c, maxDailyTelemetryRange)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rows, err := h.Store.ListDailySummaries(c.Request.Context(), id, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"vehicleId": id,
		"from":      from,
		"to":        to,
		"days":      rows,
		"count":     len(rows),
	})
}

type detectTripsBody struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (h *IoT) detectTrips(c *gin.Context) {
	id := c.Param("id")
	if !h.requireVehicleForTrack(c, id) {
		return
	}
	if h.Repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "repository not configured"})
		return
	}
	from, to, err := parseDetectTripsRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	pings, err := h.Store.Track(ctx, id, from, to, iot.MaxTrackRowLimit(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	detected := iot.DetectTripsFromPings(id, pings)
	driverID := ""
	if veh, err := h.Repo.Vehicles.Get(ctx, id); err == nil {
		driverID = veh.DriverID
	}
	existing, _ := h.Repo.Trips.List(ctx)
	created := make([]models.Trip, 0, len(detected))
	for _, d := range detected {
		if tripExists(existing, id, d.StartedAt) {
			continue
		}
		if driverID == "" {
			driverID = "UNKNOWN"
		}
		trip := models.Trip{
			ID:            generateID("TRP"),
			DriverID:      driverID,
			VehicleID:     id,
			Date:          d.StartedAt.UTC().Format("2006-01-02"),
			StartLocation: d.StartLocation,
			EndLocation:   d.EndLocation,
			DistanceKm:    d.DistanceKm,
			DurationMin:   d.DurationMin,
			FuelL:         0,
			Status:        "completed",
			StartedAt:     d.StartedAt.UTC().Format(time.RFC3339),
			EndedAt:       d.EndedAt.UTC().Format(time.RFC3339),
			AutoGenerated: true,
			Notes:         "Detected from telemetry",
		}
		saved, err := h.Repo.Trips.Add(ctx, trip)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		created = append(created, saved)
		existing = append(existing, saved)
	}
	c.JSON(http.StatusOK, gin.H{
		"vehicleId": id,
		"from":      from,
		"to":        to,
		"detected":  len(detected),
		"created":   len(created),
		"trips":     created,
	})
}

func parseDetectTripsRange(c *gin.Context) (from, to time.Time, err error) {
	now := time.Now().UTC()
	to = now
	from = now.Add(-24 * time.Hour)
	if v := c.Query("from"); v != "" {
		from, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from: %w", err)
		}
	} else if c.Request.ContentLength > 0 || c.ContentType() == "application/json" {
		var body detectTripsBody
		if err := c.ShouldBindJSON(&body); err == nil {
			if body.From != "" {
				from, err = time.Parse(time.RFC3339, body.From)
				if err != nil {
					return time.Time{}, time.Time{}, fmt.Errorf("from: %w", err)
				}
			}
			if body.To != "" {
				to, err = time.Parse(time.RFC3339, body.To)
				if err != nil {
					return time.Time{}, time.Time{}, fmt.Errorf("to: %w", err)
				}
			}
		}
	}
	if v := c.Query("to"); v != "" {
		to, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to: %w", err)
		}
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("'to' must be after 'from'")
	}
	if to.Sub(from) > iot.MaxTrackReplayRange() {
		return time.Time{}, time.Time{}, fmt.Errorf("range capped at %d days; use multiple requests", int(iot.MaxTrackReplayRange()/(24*time.Hour)))
	}
	return from, to, nil
}

func tripExists(existing []models.Trip, vehicleID string, started time.Time) bool {
	for _, t := range existing {
		if t.VehicleID != vehicleID || !t.AutoGenerated {
			continue
		}
		if t.StartedAt == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, t.StartedAt)
		if err != nil {
			continue
		}
		if ts.Equal(started) || ts.Sub(started).Abs() < time.Minute {
			return true
		}
	}
	return false
}

// stream emits Server-Sent Events for live tracking. Each connected client
// subscribes to its in-process broker AND polls the DB every 2s as a
// cross-process fallback (the TCP gateway is in another process and so its
// pings reach this loop only via the polled DB query, not the broker).
func (h *IoT) stream(c *gin.Context) {
	vehicleID := c.Param("id")
	if !h.requireVehicleForTrack(c, vehicleID) {
		return
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // disable buffering on nginx
	c.Writer.WriteHeader(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	emit := func(p iot.Ping) {
		buf, err := json.Marshal(p)
		if err != nil {
			return
		}
		fmt.Fprintf(c.Writer, "event: ping\ndata: %s\n\n", buf)
		flusher.Flush()
	}

	// Send the latest ping immediately so the client's map renders without
	// waiting for the next poll/publish.
	if p, err := h.Store.LatestPing(c.Request.Context(), vehicleID); err == nil && p != nil {
		emit(*p)
	}

	var brokerCh <-chan iot.Ping
	var brokerCancel func()
	if h.Hub != nil {
		brokerCh, brokerCancel = h.Hub.Subscribe(vehicleID)
		defer brokerCancel()
	}

	ticker := time.NewTicker(ssePollInterval)
	defer ticker.Stop()

	var lastSeenTS time.Time
	ctx := c.Request.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-brokerCh:
			if !ok {
				brokerCh = nil
				continue
			}
			if p.TS.After(lastSeenTS) {
				lastSeenTS = p.TS
			}
			emit(p)
		case <-ticker.C:
			p, err := h.Store.LatestPing(ctx, vehicleID)
			if err != nil || p == nil {
				continue
			}
			if p.TS.After(lastSeenTS) {
				lastSeenTS = p.TS
				emit(*p)
			}
			// Comment frame keeps the connection warm against idle proxies.
			fmt.Fprintf(c.Writer, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

// ────────────────────────── Fuel analytics ────────────────────────

func (h *IoT) fuelHistory(c *gin.Context) {
	id := c.Param("id")
	from, to, err := parseTrackRange(c, maxFuelTelemetryRange)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	pings, err := h.Store.FuelHistory(c.Request.Context(), id, from, to, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"vehicleId": id,
		"from":      from,
		"to":        to,
		"count":     len(pings),
		"pings":     pings,
	})
}

func (h *IoT) fuelEvents(c *gin.Context) {
	id := c.Param("id")
	from, to, err := parseTrackRange(c, maxFuelTelemetryRange)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	kind := c.Query("kind")       // refuel | drop | ""
	conf := c.Query("confidence") // high | medium | low | ""
	limit, _ := strconv.Atoi(c.Query("limit"))
	if kind != "" && kind != "refuel" && kind != "drop" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind must be 'refuel' or 'drop'"})
		return
	}
	if conf != "" && conf != "high" && conf != "medium" && conf != "low" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "confidence must be 'high', 'medium', or 'low'"})
		return
	}
	events, err := h.Store.FuelEvents(c.Request.Context(), id, from, to, kind, conf, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"vehicleId": id,
		"from":      from,
		"to":        to,
		"count":     len(events),
		"events":    events,
	})
}

func (h *IoT) fuelSummary(c *gin.Context) {
	id := c.Param("id")
	from, to, err := parseTrackRange(c, maxFuelTelemetryRange)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sum, err := h.Store.FuelSummary(c.Request.Context(), id, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sum)
}

func (h *IoT) fleetFuelAnomalies(c *gin.Context) {
	from, to, err := parseTrackRange(c, maxFuelTelemetryRange)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	events, err := h.Store.FleetFuelAnomalies(c.Request.Context(), from, to, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"from":   from,
		"to":     to,
		"count":  len(events),
		"events": events,
	})
}

// ──────────────────────────────── helpers ─────────────────────────

func parseTrackRange(c *gin.Context, maxSpan time.Duration) (from, to time.Time, err error) {
	now := time.Now().UTC()
	to = now
	from = now.Add(-1 * time.Hour) // default window: last hour

	if v := c.Query("from"); v != "" {
		from, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from: %w", err)
		}
	}
	if v := c.Query("to"); v != "" {
		to, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to: %w", err)
		}
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("'to' must be after 'from'")
	}
	if to.Sub(from) > maxSpan {
		days := int(maxSpan / (24 * time.Hour))
		if days < 1 {
			days = 1
		}
		return time.Time{}, time.Time{}, fmt.Errorf("range capped at %d days for this endpoint; use multiple requests for longer windows", days)
	}
	return from, to, nil
}

func parseTrackAfter(v string) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("after: %w", err)
	}
	return &t, nil
}

func respondIotError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, iot.ErrDeviceNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	case errors.Is(err, iot.ErrInvalidAPIKey), errors.Is(err, iot.ErrInactiveDevice):
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
