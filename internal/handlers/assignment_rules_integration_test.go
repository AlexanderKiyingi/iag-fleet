// Integration tests for driver/vehicle assignment exclusivity. Run with:
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
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

func postJSONTo(handler gin.HandlerFunc, payload any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(payload)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return w
}

func jmp(id, driver, vehicle, start, ret, status string) models.JMP {
	return models.JMP{
		ID: id, DriverID: driver, VehicleID: vehicle, Purpose: "test",
		StartDate: start, ExpectedReturn: ret, Status: status,
	}
}

// A driver can't be on two overlapping journeys (even on different vehicles).
func TestIntegration_JMPDriverDoubleBooked(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	gin.SetMode(gin.TestMode)
	j := NewJMPs(repo, "")

	if w := postJSONTo(j.create, jmp("JMP-DBK1", "DRV-DBK", "VEH-DBK1", "2030-03-01", "2030-03-05", "active")); w.Code != http.StatusCreated {
		t.Fatalf("first JMP status %d, want 201; %s", w.Code, w.Body.String())
	}
	// Overlapping window, same driver, different vehicle -> 409.
	w := postJSONTo(j.create, jmp("JMP-DBK2", "DRV-DBK", "VEH-DBK2", "2030-03-04", "2030-03-08", "active"))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "driver already") {
		t.Fatalf("overlapping-driver JMP: status %d body %q, want 409 + driver conflict", w.Code, w.Body.String())
	}
	// Non-overlapping window, same driver -> allowed.
	if w := postJSONTo(j.create, jmp("JMP-DBK3", "DRV-DBK", "VEH-DBK3", "2030-04-01", "2030-04-03", "active")); w.Code != http.StatusCreated {
		t.Fatalf("non-overlapping JMP status %d, want 201; %s", w.Code, w.Body.String())
	}
}

// A vehicle can't be booked for two overlapping journeys.
func TestIntegration_JMPVehicleDoubleBooked(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	gin.SetMode(gin.TestMode)
	j := NewJMPs(repo, "")

	if w := postJSONTo(j.create, jmp("JMP-VDB1", "DRV-VDB1", "VEH-VDB", "2030-05-01", "2030-05-04", "active")); w.Code != http.StatusCreated {
		t.Fatalf("first JMP status %d, want 201; %s", w.Code, w.Body.String())
	}
	w := postJSONTo(j.create, jmp("JMP-VDB2", "DRV-VDB2", "VEH-VDB", "2030-05-03", "2030-05-06", "active"))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "vehicle already") {
		t.Fatalf("overlapping-vehicle JMP: status %d body %q, want 409 + vehicle conflict", w.Code, w.Body.String())
	}
}

// A completed/cancelled journey frees the driver and vehicle.
func TestIntegration_JMPCompletedFreesSlot(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)
	j := NewJMPs(repo, "")

	if _, err := repo.JMPs.Add(ctx, jmp("JMP-CMP1", "DRV-CMP", "VEH-CMP", "2030-06-01", "2030-06-05", "completed")); err != nil {
		t.Fatalf("seed completed JMP: %v", err)
	}
	// Overlapping with a completed JMP is allowed.
	if w := postJSONTo(j.create, jmp("JMP-CMP2", "DRV-CMP", "VEH-CMP", "2030-06-02", "2030-06-04", "active")); w.Code != http.StatusCreated {
		t.Fatalf("overlap-with-completed status %d, want 201; %s", w.Code, w.Body.String())
	}
}

// A driver may be the assigned driver of at most one vehicle.
func TestIntegration_DriverOnOneVehicleOnly(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	if _, err := repo.Drivers.Add(ctx, models.Driver{ID: "DRV-RST", Name: "T", PermitExpiry: "2030-12-31"}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	vr := NewVehicleResource(repo, nil)
	veh := func(id, plate string) models.Vehicle {
		return models.Vehicle{
			ID: id, Plate: plate, Type: "truck", Make: "M", Model: "X", Year: 2024,
			VehicleClass: "light", Ownership: "Owned", Status: "idle", Location: "Yard",
			Capacity: "1t", LastSeen: "2026-01-01T00:00:00Z", MechStatus: "operational",
			DriverID: "DRV-RST",
		}
	}
	if w := postJSONTo(vr.create, veh("VEH-RST1", "RST-1")); w.Code != http.StatusCreated {
		t.Fatalf("first vehicle status %d, want 201; %s", w.Code, w.Body.String())
	}
	w := postJSONTo(vr.create, veh("VEH-RST2", "RST-2"))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already assigned to another vehicle") {
		t.Fatalf("second vehicle same driver: status %d body %q, want 409", w.Code, w.Body.String())
	}
}
