package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// The two derived reports, against real pings.
//
// An integration test rather than a unit one because everything that can be
// wrong here is in the SQL: the gap exclusion, the cos(latitude) in the
// distance, and the LEFT JOIN that has to keep vehicles with no telemetry at
// all.
func TestIntegration_FleetTelemetryReports(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	ctx := context.Background()
	repo := store.NewRepository(pool)
	repo.AttachTelemetry(pool)
	iotStore := iot.NewStore(pool)
	gin.SetMode(gin.TestMode)
	h := &Reports{Repo: repo}

	// One vehicle that reports, one that never has.
	live := integrationVehicle(testID("VEH-RPT-A"), "RPT-A")
	dark := integrationVehicle(testID("VEH-RPT-B"), "RPT-B")
	if _, err := repo.Vehicles.Add(ctx, live); err != nil {
		t.Fatalf("seed reporting vehicle: %v", err)
	}
	if _, err := repo.Vehicles.Add(ctx, dark); err != nil {
		t.Fatalf("seed dark vehicle: %v", err)
	}

	// Three fixes a minute apart driving east, then two hours of silence, then
	// one fix a long way off. The silence is the point: its time must not be
	// counted as driving and its hop must not be measured as distance.
	base := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Minute)
	speed := 60.0
	if _, err := iotStore.InsertPings(ctx, []iot.Ping{
		{VehicleID: live.ID, TS: base, Lat: 0.3000, Lng: 32.5000, SpeedKmh: &speed},
		{VehicleID: live.ID, TS: base.Add(time.Minute), Lat: 0.3000, Lng: 32.5090, SpeedKmh: &speed},
		{VehicleID: live.ID, TS: base.Add(2 * time.Minute), Lat: 0.3000, Lng: 32.5180, SpeedKmh: &speed},
		{VehicleID: live.ID, TS: base.Add(2*time.Hour + 2*time.Minute), Lat: 0.5000, Lng: 33.0000, SpeedKmh: &speed},
	}); err != nil {
		t.Fatalf("insert pings: %v", err)
	}

	t.Run("coverage separates never-reported from reporting", func(t *testing.T) {
		body := getReport(t, h.telemetryCoverage, "/api/reports/telemetry-coverage?days=1")
		var sawLive, sawDark bool
		for _, raw := range body["vehicles"].([]any) {
			row := raw.(map[string]any)
			switch row["vehicleId"] {
			case live.ID:
				sawLive = true
				if row["state"] != "reporting" {
					t.Fatalf("reporting vehicle state = %v", row["state"])
				}
				if got := row["pings"].(float64); got != 4 {
					t.Fatalf("pings = %v, want 4", got)
				}
				// Two of the window's hours were silence, so coverage has to
				// come in well under 100 — that ceiling is the whole point.
				if pct := row["observedPct"].(float64); pct >= 100 {
					t.Fatalf("observedPct = %v, want < 100 given a two-hour gap", pct)
				}
			case dark.ID:
				sawDark = true
				// A vehicle with no telemetry must APPEAR, or the report
				// silently shrinks to the fleet that already works — which is
				// the opposite of what it is for.
				if row["state"] != "never-reported" {
					t.Fatalf("dark vehicle state = %v, want never-reported", row["state"])
				}
				if got := row["pings"].(float64); got != 0 {
					t.Fatalf("dark vehicle pings = %v, want 0", got)
				}
			}
		}
		if !sawLive || !sawDark {
			t.Fatalf("both vehicles must appear (reporting=%v dark=%v)", sawLive, sawDark)
		}
	})

	t.Run("utilisation excludes the gap from time and distance", func(t *testing.T) {
		body := getReport(t, h.utilisation, "/api/reports/utilisation?days=1")
		for _, raw := range body["vehicles"].([]any) {
			row := raw.(map[string]any)
			if row["vehicleId"] != live.ID {
				continue
			}
			// Three one-minute hops of about a kilometre each were observed.
			// The fourth crosses the silence and must be excluded: ~2 km, not
			// the ~60 km of the straight line to where it reappeared.
			if km := row["distanceKm"].(float64); km < 1 || km > 5 {
				t.Fatalf("distanceKm = %v, want roughly 2 — a hop across the gap was counted", km)
			}
			// Two minutes of observed motion, not two hours.
			if h := row["movingHours"].(float64); h > 0.1 {
				t.Fatalf("movingHours = %v, want ~0.03 — the silence was counted as driving", h)
			}
			// And the silence is named rather than folded into "stopped": a
			// vehicle that was not reporting is not a vehicle that was parked.
			if h := row["unobservedHours"].(float64); h < 1.5 {
				t.Fatalf("unobservedHours = %v, want ~2", h)
			}
			return
		}
		t.Fatal("reporting vehicle missing from the utilisation report")
	})
}

func getReport(t *testing.T, handler gin.HandlerFunc, target string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	handler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d body %q", target, rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: bad JSON: %v", target, err)
	}
	return body
}
