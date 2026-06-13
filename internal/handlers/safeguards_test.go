package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// D1 — only out-of-service vehicles are blocked; unknown/empty is allowed.
func TestVehicleDispatchable(t *testing.T) {
	cases := []struct {
		status, mech string
		ok           bool
	}{
		{"idle", "operational", true},
		{"moving", "", true},
		{"", "", true},
		{"offline", "", false},
		{"maintenance", "", false},
		{"idle", "grounded", false},
		{"idle", "out-of-service", false},
	}
	for _, tc := range cases {
		err := vehicleDispatchable(models.Vehicle{Status: tc.status, MechStatus: tc.mech})
		if (err == nil) != tc.ok {
			t.Errorf("status=%q mech=%q: err=%v, want ok=%v", tc.status, tc.mech, err, tc.ok)
		}
	}
}

// D2 — medical always gates; first-aid/defensive gate only when carried.
func TestDriverCertsOK(t *testing.T) {
	now := time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)
	const past, future = "2020-01-01", "2030-01-01"
	cases := []struct {
		name string
		d    models.Driver
		ok   bool
	}{
		{"all clear", models.Driver{MedicalExpiry: future}, true},
		{"medical unset", models.Driver{}, true},
		{"medical expired", models.Driver{MedicalExpiry: past}, false},
		{"firstaid expired but not carried", models.Driver{FirstAidExpiry: past}, true},
		{"firstaid expired and carried", models.Driver{FirstAid: true, FirstAidExpiry: past}, false},
		{"defensive expired and carried", models.Driver{Defensive: true, DefensiveExpiry: past}, false},
	}
	for _, tc := range cases {
		if err := driverCertsOK(tc.d, now); (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// N2 — non-negative quantities + internally consistent total.
func TestValidateFuelValues(t *testing.T) {
	cases := []struct {
		name string
		rec  models.FuelRecord
		ok   bool
	}{
		{"valid", models.FuelRecord{Litres: 10, UnitPrice: 5000, Total: 50000}, true},
		{"rounding tolerated", models.FuelRecord{Litres: 10, UnitPrice: 5000, Total: 50001}, true},
		{"zero price skips consistency", models.FuelRecord{Litres: 10, UnitPrice: 0, Total: 0}, true},
		{"negative litres", models.FuelRecord{Litres: -1, UnitPrice: 5000}, false},
		{"negative total", models.FuelRecord{Litres: 10, UnitPrice: 5000, Total: -5}, false},
		{"inconsistent total", models.FuelRecord{Litres: 10, UnitPrice: 5000, Total: 99999}, false},
	}
	for _, tc := range cases {
		rec := tc.rec
		if err := validateFuelValues(&rec); (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// N1 — completing a work order twice is rejected (would double-decrement stock).
func TestIntegration_MaintenanceCompleteOnce(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	gin.SetMode(gin.TestMode)
	w := &Workflows{Repo: repo}

	mx := models.MaintenanceItem{
		ID: "MX-CMPL", VehicleID: "VEH-CMPL", Date: "2026-06-13", Type: "Service",
		Service: "oil", Status: "in-progress", Priority: "normal", Workshop: "W1",
	}
	if _, err := repo.Maintenance.Add(context.Background(), mx); err != nil {
		t.Fatalf("seed maintenance: %v", err)
	}
	call := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Params = gin.Params{{Key: "id", Value: mx.ID}}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/maintenance/"+mx.ID+"/complete", nil)
		w.maintenanceComplete(c)
		return rec
	}
	if r := call(); r.Code != http.StatusOK {
		t.Fatalf("first complete status %d, want 200; %s", r.Code, r.Body.String())
	}
	if r := call(); r.Code != http.StatusConflict {
		t.Fatalf("second complete status %d, want 409; %s", r.Code, r.Body.String())
	}
}
