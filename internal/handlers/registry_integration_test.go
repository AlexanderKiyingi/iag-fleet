// Integration tests against a real PostgreSQL instance. Run with:
//
//	TEST_DATABASE_URL=postgres://svc_iag_fleet:iag_fleet_dev@localhost:5432/iag_platform?sslmode=disable \
//	  go test ./internal/handlers/... -run Integration -v
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

func integrationVehicle(id, plate string) models.Vehicle {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return models.Vehicle{
		ID: id, Plate: plate, Type: "truck", Make: "Test", Model: "X",
		Year: 2024, VehicleClass: "light", Ownership: "Owned",
		Status: "idle", Location: "Yard", Lat: 0.3476, Lng: 32.5825,
		Capacity: "1t", LastSeen: now, MechStatus: "operational",
	}
}

func TestIntegration_VehicleCRUD(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()

	repo := store.NewRepository(pool)
	ctx := context.Background()
	v := integrationVehicle("VEH-INT1", "INT-001")

	created, err := repo.Vehicles.Add(ctx, v)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Plate != v.Plate {
		t.Fatalf("plate %q", created.Plate)
	}

	got, err := repo.Vehicles.Get(ctx, v.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != v.ID {
		t.Fatalf("id %q", got.ID)
	}

	if err := repo.Vehicles.Delete(ctx, v.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Vehicles.Get(ctx, v.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestIntegration_DuplicatePlateConflict(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()

	repo := store.NewRepository(pool)
	ctx := context.Background()
	if _, err := repo.Vehicles.Add(ctx, integrationVehicle("VEH-A", "DUP-PLATE")); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err := repo.Vehicles.Add(ctx, integrationVehicle("VEH-B", "DUP-PLATE"))
	if err == nil {
		t.Fatal("expected duplicate plate error")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("want 23505, got %v", err)
	}
}

func TestIntegration_IoTBindUnknownVehicle404(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()

	repo := store.NewRepository(pool)
	h := &IoT{Store: iot.NewStore(pool), Repo: repo}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]any{
		"serial": "IMEI-INT-1", "vehicleId": "VEH-MISSING", "issueKey": false,
	})
	c.Request = httptest.NewRequest(http.MethodPost, "/api/iot/devices", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.createDevice(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bind unknown vehicle: status %d body %s", w.Code, w.Body.String())
	}
}

func TestIntegration_TrackUnknownVehicle404(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()

	repo := store.NewRepository(pool)
	h := &IoT{Store: iot.NewStore(pool), Repo: repo}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/vehicles/VEH-MISSING/track", nil)
	c.Params = gin.Params{{Key: "id", Value: "VEH-MISSING"}}

	if h.requireVehicleForTrack(c, "VEH-MISSING") {
		t.Fatal("expected validation failure")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("track unknown vehicle: status %d", w.Code)
	}
}

func TestIntegration_SyncVehicleStatusOutbox(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	t.Setenv("EVENT_BUS_ENABLED", "true")

	ctx := context.Background()
	repo := store.NewRepository(pool)
	v := integrationVehicle("VEH-SYNC", "SYNC-01")
	v.Status = "offline"
	if _, err := repo.Vehicles.Add(ctx, v); err != nil {
		t.Fatalf("seed vehicle: %v", err)
	}

	iotStore := iot.NewStore(pool)
	speed := 20.0
	syncRes, err := iotStore.SyncVehicleFromPing(ctx, iot.Ping{
		VehicleID: v.ID, TS: time.Now().UTC(), Lat: 0.35, Lng: 32.59, SpeedKmh: &speed,
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !syncRes.Changed || syncRes.NewStatus != "moving" {
		t.Fatalf("sync result: %+v", syncRes)
	}
	if err := iotStore.PublishStatusChange(ctx, syncRes); err != nil {
		t.Fatalf("publish: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM fleet_event_outbox
		WHERE event_type = 'fleet.vehicle.status_changed' AND event_key = $1
	`, v.ID).Scan(&count); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if count != 1 {
		t.Fatalf("outbox rows %d, want 1", count)
	}
}
