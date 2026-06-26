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
	"github.com/google/uuid"
	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/events"
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
	Hub    *iot.Hub
	Repo   *store.Repository // optional: vehicle validation + list enrichment; nil in tests
	Events *events.Bus       // optional: registry events from test-ping path
	Gate   *StreamGate       // optional: caps concurrent SSE streams; nil = unlimited
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

	// Driver companion-app self-report: the driver's phone posts its GPS while on
	// an active journey. Authenticated by the driver's platform JWT (no device
	// key); the vehicle is resolved from the driver's active JMP, not the body.
	rg.POST("/me/location", auth.RequireUser(), h.requireStore, h.driverLocation)

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
		respondIotError(c, err)
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
	syncRes, err := h.Store.SyncVehicleFromPing(ctx, p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Fleet API outbox only — do not also call PublishStatusChange (would duplicate rows).
	if h.Events != nil && syncRes.Changed {
		h.Events.PublishFleet(ctx, events.TypeVehicleStatusChanged, events.FleetEventData(map[string]string{
			"vehicleId":      syncRes.VehicleID,
			"status":         syncRes.NewStatus,
			"previousStatus": syncRes.PreviousStatus,
			"source":         "test_ping",
		}), syncRes.VehicleID, "")
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

// ─────────────────────── Driver companion-app GPS ──────────────────────

type driverLocationBody struct {
	Lat      float64  `json:"lat"`
	Lng      float64  `json:"lng"`
	Heading  *float64 `json:"heading,omitempty"`
	SpeedKmh *float64 `json:"speedKmh,omitempty"`
	Accuracy *float64 `json:"accuracy,omitempty"` // GPS accuracy in metres; carried in raw
	TS       *string  `json:"ts,omitempty"`       // RFC3339; defaults to now()
}

var (
	errNoDriverProfile    = errors.New("no driver profile linked to this account")
	errNoActiveAssignment = errors.New("no active assignment")
)

// driverLocation ingests one GPS fix self-reported by a driver's phone. The
// driver is identified by their platform JWT; the target vehicle is the one on
// their active journey (JMP). It reuses the exact device-ingest pipeline
// (InsertPings → ApplyVehicleHotState → Hub.Publish → geofences) so the vehicle
// registry position stays fresh exactly as it does for hardware trackers.
func (h *IoT) driverLocation(c *gin.Context) {
	if h.Repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "repository not configured"})
		return
	}
	ctx := c.Request.Context()

	vehicleID, err := h.resolveDriverActiveVehicle(ctx, c)
	if err != nil {
		switch {
		case errors.Is(err, errNoDriverProfile):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		case errors.Is(err, errNoActiveAssignment):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	var body driverLocationBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Lat < -90 || body.Lat > 90 || body.Lng < -180 || body.Lng > 180 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lat/lng out of range"})
		return
	}
	if body.Lat == 0 && body.Lng == 0 {
		// 0,0 is the device "no fix" sentinel — reject rather than teleport the
		// vehicle into the Gulf of Guinea.
		c.JSON(http.StatusBadRequest, gin.H{"error": "no GPS fix"})
		return
	}

	ts := time.Now().UTC()
	if body.TS != nil && *body.TS != "" {
		parsed, perr := time.Parse(time.RFC3339, *body.TS)
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ts: " + perr.Error()})
			return
		}
		if parsed.After(time.Now().Add(5 * time.Minute)) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ts is in the future"})
			return
		}
		ts = parsed.UTC()
	}

	raw := json.RawMessage(`{"source":"mobile"}`)
	if body.Accuracy != nil {
		if b, mErr := json.Marshal(map[string]any{"source": "mobile", "accuracyM": *body.Accuracy}); mErr == nil {
			raw = b
		}
	}

	p := iot.Ping{
		VehicleID: vehicleID,
		DeviceID:  nil, // device-less: this is a phone, not a paired tracker
		TS:        ts,
		Lat:       body.Lat,
		Lng:       body.Lng,
		Heading:   body.Heading,
		SpeedKmh:  body.SpeedKmh,
		Raw:       raw,
	}
	if _, err := h.Store.InsertPings(ctx, []iot.Ping{p}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// ApplyVehicleHotState syncs the vehicle row AND enqueues the status-change
	// outbox event in one transaction — do not also publish via h.Events here or
	// the status change would be emitted twice.
	if _, err := h.Store.ApplyVehicleHotState(ctx, p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.Hub != nil {
		h.Hub.Publish(p)
	}
	_ = h.Store.ApplyGeofenceTransitions(ctx, iot.ProcessGeofences(p))

	latest, err := h.Store.LatestPing(ctx, vehicleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "vehicleId": vehicleID, "ping": latest})
}

// resolveDriverActiveVehicle maps the authenticated platform user to a driver
// row (by platform_user_id) and then to the vehicle on their active journey.
// Returns errNoDriverProfile (→403) when no driver is linked to the account and
// errNoActiveAssignment (→409) when the driver has no active JMP covering today.
func (h *IoT) resolveDriverActiveVehicle(ctx context.Context, c *gin.Context) (string, error) {
	uid, ok := auth.PlatformUserID(c)
	if !ok {
		return "", errNoDriverProfile
	}
	drivers, _, err := h.Repo.Drivers.ListFiltered(ctx, store.ListFilter{
		Filters: map[string]string{"platform_user_id": uid.String()},
		Limit:   2,
	})
	if err != nil {
		return "", err
	}
	var driverID string
	if len(drivers) > 0 {
		driverID = drivers[0].ID
	} else {
		// Not linked yet: one-time bootstrap by email. The platform JWT carries
		// an authenticated (subject, email) pair, so an email match lets us stamp
		// the durable platform_user_id link without an admin step. Email is used
		// only here, once — every subsequent request resolves by UUID.
		driverID, err = h.bootstrapDriverLink(ctx, uid, c)
		if err != nil {
			return "", err
		}
	}

	jmps, _, err := h.Repo.JMPs.ListFiltered(ctx, store.ListFilter{
		Filters: map[string]string{"driver_id": driverID, "status": "active"},
		Limit:   1000,
	})
	if err != nil {
		return "", err
	}
	today := time.Now().UTC().Format("2006-01-02")
	best := ""
	bestStart := ""
	for _, j := range jmps {
		if j.VehicleID == "" || j.StartDate == "" {
			continue
		}
		if today < j.StartDate || today > jmpWindowEnd(j) {
			continue
		}
		// Prefer the most recently started journey when several overlap.
		if best == "" || j.StartDate > bestStart {
			best = j.VehicleID
			bestStart = j.StartDate
		}
	}
	if best == "" {
		return "", errNoActiveAssignment
	}
	return best, nil
}

// bootstrapDriverLink stamps platform_user_id on the single unlinked driver
// whose email matches the caller's JWT, returning that driver's id. Returns
// errNoDriverProfile when there is no email, no match, or an ambiguous match
// (more than one unlinked driver shares the email) — auto-linking only proceeds
// when it is unambiguous; the rest must be linked manually by an admin.
func (h *IoT) bootstrapDriverLink(ctx context.Context, uid uuid.UUID, c *gin.Context) (string, error) {
	claims, ok := auth.PlatformClaimsFromContext(c)
	if !ok || claims.Email == "" {
		return "", errNoDriverProfile
	}
	pool := h.Repo.Pool()
	rows, err := pool.Query(ctx, `
		SELECT id FROM drivers
		WHERE platform_user_id IS NULL AND lower(email) = lower($1)
		LIMIT 2`, claims.Email)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) != 1 {
		return "", errNoDriverProfile
	}
	// Guard the WHERE on still-NULL so a concurrent bootstrap can't double-stamp;
	// the loser sees 0 rows affected and falls back to errNoDriverProfile (its
	// retry then resolves cleanly by UUID).
	ct, err := pool.Exec(ctx, `
		UPDATE drivers SET platform_user_id = $1
		WHERE id = $2 AND platform_user_id IS NULL`, uid, ids[0])
	if err != nil {
		return "", err
	}
	if ct.RowsAffected() == 0 {
		return "", errNoDriverProfile
	}
	return ids[0], nil
}

// jmpWindowEnd is the inclusive last date a JMP is considered active, mirroring
// the fuel-reconciliation window (internal/jmp/reconcile.go): completion date if
// set, else expected return, else expected arrival, else the start date.
func jmpWindowEnd(j models.JMP) string {
	if len(j.CompletedAt) >= 10 {
		return j.CompletedAt[:10]
	}
	if j.ExpectedReturn != "" {
		return j.ExpectedReturn
	}
	if j.ExpectedArrival != "" {
		return j.ExpectedArrival
	}
	return j.StartDate
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
	release, ok := h.Gate.reserveStream(c)
	if !ok {
		return
	}
	defer release()

	vehicleID := c.Param("id")
	if !h.requireVehicleForTrack(c, vehicleID) {
		return
	}
	expiry := tokenExpiry(c)
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
	var pollFails int
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
			// Close once the bearer token lapses so a stream can't outlive its
			// auth; the client reconnects with a fresh token.
			if tokenExpired(expiry, time.Now()) {
				sseEvent(c.Writer, flusher, "expired", `{"reason":"token expired; reconnect"}`)
				return
			}
			p, err := h.Store.LatestPing(ctx, vehicleID)
			switch {
			case err != nil:
				// Don't hang alive-but-empty: surface and close after a run of
				// failures instead of silently swallowing every poll error.
				pollFails++
				if pollFails >= maxConsecutivePollFails {
					sseEvent(c.Writer, flusher, "error", `{"reason":"telemetry temporarily unavailable"}`)
					return
				}
			case p != nil && p.TS.After(lastSeenTS):
				pollFails = 0
				lastSeenTS = p.TS
				emit(*p)
			default:
				pollFails = 0
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
	if name, ok := uniqueConstraint(err); ok {
		switch name {
		case "iot_devices_one_active_per_vehicle":
			c.JSON(http.StatusConflict, gin.H{"error": "vehicle already has an active device; deactivate or reassign it first"})
		default:
			c.JSON(http.StatusConflict, gin.H{"error": "serial already registered"})
		}
		return
	}
	switch {
	case errors.Is(err, iot.ErrDeviceNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	case errors.Is(err, iot.ErrInvalidAPIKey), errors.Is(err, iot.ErrInactiveDevice):
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
