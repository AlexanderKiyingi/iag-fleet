// Integration tests for referential-integrity / delete guards (S2) and tyre
// position uniqueness (U1). Run with TEST_DATABASE_URL set.
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

func deleteCall(handler gin.HandlerFunc, id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/x/"+id, nil)
	handler(c)
	return w
}

// S2: a journey can't reference a vehicle that doesn't exist.
func TestIntegration_JMPRejectsUnknownVehicle(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	gin.SetMode(gin.TestMode)
	j := NewJMPs(repo, "")

	w := postJSONTo(j.create, models.JMP{
		ID: "JMP-REF", VehicleID: "VEH-NOPE", Purpose: "x",
		StartDate: "2032-01-01", ExpectedReturn: "2032-01-02", Status: "draft",
	})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "vehicle not found") {
		t.Fatalf("unknown-vehicle JMP: status %d body %q, want 400", w.Code, w.Body.String())
	}
}

// S2: deleting a vehicle/driver referenced by a live journey is blocked.
func TestIntegration_DeleteBlockedByLiveJMP(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	if _, err := repo.JMPs.Add(ctx, models.JMP{
		ID: "JMP-DEL", VehicleID: "VEH-DEL", DriverID: "DRV-DEL", Purpose: "x",
		StartDate: "2032-02-01", ExpectedReturn: "2032-02-03", Status: "active",
	}); err != nil {
		t.Fatalf("seed jmp: %v", err)
	}

	vr := NewVehicleResource(repo, nil)
	if w := deleteCall(vr.remove, "VEH-DEL"); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "live journey") {
		t.Fatalf("vehicle delete: status %d body %q, want 409", w.Code, w.Body.String())
	}
	dr := NewDriverResource(repo)
	if w := deleteCall(dr.remove, "DRV-DEL"); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "live journey") {
		t.Fatalf("driver delete: status %d body %q, want 409", w.Code, w.Body.String())
	}
}

// S2: bulk delete must honor the same live-journey guard as single delete —
// the referenced vehicle is reported in "blocked" while the free one is
// removed. Regression test for bulkDelete bypassing BeforeDelete.
func TestIntegration_BulkDeleteHonorsGuard(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	if _, err := repo.Vehicles.Add(ctx, integrationVehicle("VEH-BUSY", "BSY-1")); err != nil {
		t.Fatalf("seed busy vehicle: %v", err)
	}
	if _, err := repo.Vehicles.Add(ctx, integrationVehicle("VEH-FREE", "FRE-1")); err != nil {
		t.Fatalf("seed free vehicle: %v", err)
	}
	if _, err := repo.JMPs.Add(ctx, models.JMP{
		ID: "JMP-BULK", VehicleID: "VEH-BUSY", Purpose: "x",
		StartDate: "2032-03-01", ExpectedReturn: "2032-03-03", Status: "active",
	}); err != nil {
		t.Fatalf("seed jmp: %v", err)
	}

	vr := NewVehicleResource(repo, nil)
	w := postJSONTo(vr.bulkDelete, gin.H{"ids": []string{"VEH-BUSY", "VEH-FREE"}})
	if w.Code != http.StatusOK {
		t.Fatalf("bulk delete: status %d, want 200; %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"deleted":1`) {
		t.Fatalf("bulk delete: want deleted:1, got %s", body)
	}
	if !strings.Contains(body, "VEH-BUSY") || !strings.Contains(body, "live journey") {
		t.Fatalf("bulk delete: want VEH-BUSY blocked by live journey, got %s", body)
	}
	// The free vehicle is gone; the busy one survives the guard.
	if _, err := repo.Vehicles.Get(ctx, "VEH-FREE"); err == nil {
		t.Fatalf("VEH-FREE should have been deleted")
	}
	if _, err := repo.Vehicles.Get(ctx, "VEH-BUSY"); err != nil {
		t.Fatalf("VEH-BUSY should have survived the guard, got %v", err)
	}
}

// U1: one current tyre per (vehicle, position); retired tyres don't block.
func TestIntegration_TyrePositionUnique(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	if _, err := repo.Vehicles.Add(ctx, integrationVehicle("VEH-TYR", "TYR-1")); err != nil {
		t.Fatalf("seed vehicle: %v", err)
	}
	tr := NewTyreResource(repo)
	mk := func(id, pos, status string) models.Tyre {
		return models.Tyre{ID: id, VehicleID: "VEH-TYR", Position: pos, Status: status, Brand: "B"}
	}
	if w := postJSONTo(tr.create, mk("TR1", "FL", "good")); w.Code != http.StatusCreated {
		t.Fatalf("first FL tyre: status %d; %s", w.Code, w.Body.String())
	}
	if w := postJSONTo(tr.create, mk("TR2", "FL", "good")); w.Code != http.StatusConflict {
		t.Fatalf("second FL tyre: status %d, want 409; %s", w.Code, w.Body.String())
	}
	if w := postJSONTo(tr.create, mk("TR3", "FR", "good")); w.Code != http.StatusCreated {
		t.Fatalf("FR tyre: status %d, want 201; %s", w.Code, w.Body.String())
	}
	// A retired tyre at a position doesn't block a fresh mount there.
	if _, err := repo.Tyres.Add(ctx, mk("TR4", "RL", "replaced")); err != nil {
		t.Fatalf("seed retired tyre: %v", err)
	}
	if w := postJSONTo(tr.create, mk("TR5", "RL", "good")); w.Code != http.StatusCreated {
		t.Fatalf("RL tyre over retired: status %d, want 201; %s", w.Code, w.Body.String())
	}
	// Unknown vehicle is rejected.
	if w := postJSONTo(tr.create, models.Tyre{ID: "TR6", VehicleID: "NOPE", Position: "RR"}); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown-vehicle tyre: status %d, want 400; %s", w.Code, w.Body.String())
	}
}
