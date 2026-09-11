package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/config"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// The simulator has to leave a trace.
//
// It used to write lat/lng/heading/last_seen straight onto the vehicle row and
// stop there. The vehicle moved on the map and recorded nothing: no ping, so no
// track, no trail, no trip detection, no geofence transition. A simulated fleet
// exercised none of the features it exists to demonstrate, and produced exactly
// the report "the car is moving on the map but its path is never plotted".
func TestIntegration_SimulateTickRecordsTelemetry(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	ctx := context.Background()
	repo := store.NewRepository(pool)
	iotStore := iot.NewStore(pool)
	gin.SetMode(gin.TestMode)

	// Simulation is refused outright on a production deployment, so the config
	// under test has to be one where it is allowed.
	w := &Workflows{Repo: repo, Config: config.Config{}, IoTStore: iotStore}

	v := integrationVehicle(testID("VEH-SIM"), "SIM-01")
	v.Status = "moving"
	v.Lat, v.Lng, v.Heading, v.Speed = 0.3476, 32.5825, 90, 40
	if _, err := repo.Vehicles.Add(ctx, v); err != nil {
		t.Fatalf("seed vehicle: %v", err)
	}

	from := time.Now().UTC().Add(-time.Minute)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/vehicles/simulate-tick", nil)
	w.simulateTick(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %q, want 200", rec.Code, rec.Body.String())
	}

	// The point of the change: a fix that can be read back as history.
	pings, err := iotStore.Track(ctx, v.ID, from, time.Now().UTC().Add(time.Minute), 100, nil)
	if err != nil {
		t.Fatalf("track: %v", err)
	}
	if len(pings) == 0 {
		t.Fatal("simulate-tick wrote no telemetry: the vehicle moved and left no history")
	}

	// Tagged at the source. Once it is a row of numbers a simulated fix is
	// indistinguishable from a real one, and whoever reads this history later
	// is entitled to know which it was.
	if got := string(pings[0].Raw); !strings.Contains(got, `"source":"simulator"`) {
		t.Fatalf("ping raw = %q, want it tagged as simulator-sourced", got)
	}

	// ApplyVehicleHotState must still move the registry row, or the map stops
	// working in exchange for the history.
	got, err := repo.Vehicles.Get(ctx, v.ID)
	if err != nil {
		t.Fatalf("get vehicle: %v", err)
	}
	if got.Lat == v.Lat && got.Lng == v.Lng {
		t.Fatalf("vehicle did not move: still at %v,%v", got.Lat, got.Lng)
	}
	if got.Lat != pings[len(pings)-1].Lat || got.Lng != pings[len(pings)-1].Lng {
		t.Fatalf("registry %v,%v disagrees with the ping %v,%v — the map and the trail would diverge",
			got.Lat, got.Lng, pings[len(pings)-1].Lat, pings[len(pings)-1].Lng)
	}
}

// Without a telemetry store the simulator must still move vehicles, or a
// deployment with no telemetry tables loses its map as well as its history.
func TestIntegration_SimulateTickWithoutTelemetryStillMoves(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	ctx := context.Background()
	repo := store.NewRepository(pool)
	gin.SetMode(gin.TestMode)

	w := &Workflows{Repo: repo, Config: config.Config{}} // IoTStore nil

	v := integrationVehicle(testID("VEH-SIM-NOSTORE"), "SIM-02")
	v.Status = "moving"
	v.Lat, v.Lng, v.Heading, v.Speed = 0.3476, 32.5825, 90, 40
	if _, err := repo.Vehicles.Add(ctx, v); err != nil {
		t.Fatalf("seed vehicle: %v", err)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/vehicles/simulate-tick", nil)
	w.simulateTick(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %q, want 200", rec.Code, rec.Body.String())
	}
	got, err := repo.Vehicles.Get(ctx, v.ID)
	if err != nil {
		t.Fatalf("get vehicle: %v", err)
	}
	if got.Lat == v.Lat && got.Lng == v.Lng {
		t.Fatal("fallback path did not move the vehicle")
	}
}
