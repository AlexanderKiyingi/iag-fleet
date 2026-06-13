// Integration tests for the HAULA feature-parity additions. Run with:
//
//	TEST_DATABASE_URL=postgres://svc_iag_fleet:iag_fleet_dev@localhost:5432/iag_platform?sslmode=disable \
//	  go test ./internal/handlers/... -run Integration -v
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

func ptrF(v float64) *float64 { return &v }
func ptrI(v int) *int         { return &v }

// Round-trip the new SafetyEvent + MaintenanceItem columns to prove migration
// 0021 applied and the reflective store picked up the new db-tagged fields.
func TestIntegration_HaulaParityFieldsRoundTrip(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()

	se := models.SafetyEvent{
		ID: "SE-INT1", VehicleID: "VEH-INT", Date: "2026-06-13T00:00:00Z",
		Type: "Mechanical failure", Severity: "crit", Status: "open",
		Description: "Engine cut-out", ReportedBy: "Driver",
		GpsLat: ptrF(-0.795), GpsLng: ptrF(30.180), Injuries: ptrI(0),
		Cost: ptrF(850000), Authorities: "Police post Ntungamo",
	}
	if _, err := repo.Safety.Add(ctx, se); err != nil {
		t.Fatalf("add safety: %v", err)
	}
	got, err := repo.Safety.Get(ctx, se.ID)
	if err != nil {
		t.Fatalf("get safety: %v", err)
	}
	if got.GpsLat == nil || *got.GpsLat != -0.795 || got.Cost == nil || *got.Cost != 850000 || got.Authorities != se.Authorities {
		t.Fatalf("safety new fields did not round-trip: %+v", got)
	}

	mx := models.MaintenanceItem{
		ID: "MX-INT1", VehicleID: "VEH-INT", Date: "2026-06-13", Type: "Repair",
		Service: "x", Status: "scheduled", Priority: "high", Workshop: "W1",
		Mechanic: "J. Mukasa", LinkedSafetyID: "SE-INT1",
	}
	if _, err := repo.Maintenance.Add(ctx, mx); err != nil {
		t.Fatalf("add maintenance: %v", err)
	}
	gotMx, err := repo.Maintenance.Get(ctx, mx.ID)
	if err != nil {
		t.Fatalf("get maintenance: %v", err)
	}
	if gotMx.Mechanic != "J. Mukasa" || gotMx.LinkedSafetyID != "SE-INT1" {
		t.Fatalf("maintenance new fields did not round-trip: %+v", gotMx)
	}
}

// safetyCreateWO raises a linked WO and is idempotent.
func TestIntegration_SafetyCreateWO(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	se := models.SafetyEvent{
		ID: "SE-INT2", VehicleID: "VEH-INT", Date: "2026-06-13T00:00:00Z",
		Type: "Mechanical failure", Severity: "crit", Status: "open",
		Description: "Brake failure", ReportedBy: "Driver",
	}
	if _, err := repo.Safety.Add(ctx, se); err != nil {
		t.Fatalf("add safety: %v", err)
	}

	wf := &Workflows{Repo: repo}
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: se.ID}}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/safety/"+se.ID+"/create-wo", nil)
		wf.safetyCreateWO(c)
		return w
	}

	if w := call(); w.Code != http.StatusCreated {
		t.Fatalf("first create-wo status %d, want 201; body=%s", w.Code, w.Body.String())
	}
	// Both sides linked.
	gotSe, _ := repo.Safety.Get(ctx, se.ID)
	if gotSe.LinkedWoID == "" {
		t.Fatalf("safety.linkedWoId not set")
	}
	gotMx, err := repo.Maintenance.Get(ctx, gotSe.LinkedWoID)
	if err != nil || gotMx.LinkedSafetyID != se.ID || gotMx.Priority != "critical" {
		t.Fatalf("linked WO wrong: %+v err=%v", gotMx, err)
	}
	// Idempotent: second call returns 200 alreadyLinked, no new WO.
	if w := call(); w.Code != http.StatusOK {
		t.Fatalf("second create-wo status %d, want 200; body=%s", w.Code, w.Body.String())
	}
	all, _ := repo.Maintenance.List(ctx)
	n := 0
	for _, m := range all {
		if m.LinkedSafetyID == se.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 WO linked to incident, got %d", n)
	}
}

// Fuel "confirm" sets anomalyStatus=confirmed and records the lifecycle event;
// an unknown event is rejected.
func TestIntegration_FuelConfirmAnomaly(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	yes := true
	fr := models.FuelRecord{
		ID: "FR-INT1", VehicleID: "VEH-INT", Date: "2026-06-13",
		Litres: 10, UnitPrice: 5000, Total: 50000, Station: "Shell",
		Anomaly: &yes, AnomalyStatus: "open",
	}
	if _, err := repo.Fuel.Add(ctx, fr); err != nil {
		t.Fatalf("add fuel: %v", err)
	}

	f := NewFuelRecords(repo, nil)
	post := func(event string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: fr.ID}}
		body, _ := json.Marshal(map[string]string{"event": event})
		c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		f.anomalyEvent(c)
		return w
	}

	if w := post("confirm"); w.Code != http.StatusOK {
		t.Fatalf("confirm status %d, want 200; body=%s", w.Code, w.Body.String())
	}
	got, _ := repo.Fuel.Get(ctx, fr.ID)
	if got.AnomalyStatus != "confirmed" {
		t.Fatalf("anomalyStatus %q, want confirmed", got.AnomalyStatus)
	}
	foundConfirmed := false
	for _, e := range got.AnomalyHistory {
		if e.Event == "confirmed" {
			foundConfirmed = true
		}
	}
	if !foundConfirmed {
		t.Fatalf("no 'confirmed' anomaly-history event: %+v", got.AnomalyHistory)
	}

	if w := post("bogus"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown event status %d, want 400", w.Code)
	}
}
