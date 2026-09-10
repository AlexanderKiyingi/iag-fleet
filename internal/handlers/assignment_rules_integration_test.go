// Integration tests for driver/vehicle assignment exclusivity. Run with:
//
//	TEST_DATABASE_URL=postgres://svc_iag_fleet:iag_fleet_dev@localhost:5432/iag_platform?sslmode=disable \
//	  go test ./internal/handlers/... -run Integration -v
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// These tests are about double-booking, not the toolbox gate, so the toolbox
// talk is pre-completed — activating a journey without it is a 409 handled by
// TestIntegration_JMPActiveRequiresToolbox.
func jmp(id, driver, vehicle, start, ret, status string) models.JMP {
	j := integrationJMP(id, vehicle, driver, start, ret, status)
	j.Toolbox = models.Toolbox{Completed: true}
	return j
}

// seedCrew creates the drivers and vehicles a set of JMPs reference. The create
// handler enforces referential integrity, so an unseeded id fails the booking
// assertion with a 400 long before any overlap check runs.
func seedCrew(t *testing.T, repo *store.Repository, drivers, vehicles []string) {
	t.Helper()
	ctx := context.Background()
	for _, d := range drivers {
		if _, err := repo.Drivers.Add(ctx, integrationDriver(d)); err != nil {
			t.Fatalf("seed driver %s: %v", d, err)
		}
	}
	for i, v := range vehicles {
		if _, err := repo.Vehicles.Add(ctx, integrationVehicle(v, fmt.Sprintf("PL-%d-%s", i, v))); err != nil {
			t.Fatalf("seed vehicle %s: %v", v, err)
		}
	}
}

// A driver can't be on two overlapping journeys (even on different vehicles).
func TestIntegration_JMPDriverDoubleBooked(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	gin.SetMode(gin.TestMode)
	j := NewJMPs(repo, "")
	seedCrew(t, repo, []string{testID("DRV-DBK")}, []string{testID("VEH-DBK1"), testID("VEH-DBK2"), testID("VEH-DBK3")})

	if w := postJSONTo(j.create, jmp(testID("JMP-DBK1"), testID("DRV-DBK"), testID("VEH-DBK1"), "2030-03-01", "2030-03-05", "active")); w.Code != http.StatusCreated {
		t.Fatalf("first JMP status %d, want 201; %s", w.Code, w.Body.String())
	}
	// Overlapping window, same driver, different vehicle -> 409.
	w := postJSONTo(j.create, jmp(testID("JMP-DBK2"), testID("DRV-DBK"), testID("VEH-DBK2"), "2030-03-04", "2030-03-08", "active"))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "driver already") {
		t.Fatalf("overlapping-driver JMP: status %d body %q, want 409 + driver conflict", w.Code, w.Body.String())
	}
	// Non-overlapping window, same driver -> allowed.
	if w := postJSONTo(j.create, jmp(testID("JMP-DBK3"), testID("DRV-DBK"), testID("VEH-DBK3"), "2030-04-01", "2030-04-03", "active")); w.Code != http.StatusCreated {
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
	seedCrew(t, repo, []string{testID("DRV-VDB1"), testID("DRV-VDB2")}, []string{testID("VEH-VDB")})

	if w := postJSONTo(j.create, jmp(testID("JMP-VDB1"), testID("DRV-VDB1"), testID("VEH-VDB"), "2030-05-01", "2030-05-04", "active")); w.Code != http.StatusCreated {
		t.Fatalf("first JMP status %d, want 201; %s", w.Code, w.Body.String())
	}
	w := postJSONTo(j.create, jmp(testID("JMP-VDB2"), testID("DRV-VDB2"), testID("VEH-VDB"), "2030-05-03", "2030-05-06", "active"))
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
	seedCrew(t, repo, []string{testID("DRV-CMP")}, []string{testID("VEH-CMP")})

	if _, err := repo.JMPs.Add(ctx, jmp(testID("JMP-CMP1"), testID("DRV-CMP"), testID("VEH-CMP"), "2030-06-01", "2030-06-05", "completed")); err != nil {
		t.Fatalf("seed completed JMP: %v", err)
	}
	// Overlapping with a completed JMP is allowed.
	if w := postJSONTo(j.create, jmp(testID("JMP-CMP2"), testID("DRV-CMP"), testID("VEH-CMP"), "2030-06-02", "2030-06-04", "active")); w.Code != http.StatusCreated {
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

	if _, err := repo.Drivers.Add(ctx, models.Driver{ID: testID("DRV-RST"), Name: "T", PermitExpiry: "2030-12-31"}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	vr := NewVehicleResource(repo, nil)
	veh := func(id, plate string) models.Vehicle {
		return models.Vehicle{
			ID: id, Plate: plate, Type: "truck", Make: "M", Model: "X", Year: 2024,
			VehicleClass: "light", Ownership: "Owned", Status: "idle", Location: "Yard",
			Capacity: "1t", LastSeen: "2026-01-01T00:00:00Z", MechStatus: "operational",
			DriverID: testID("DRV-RST"),
		}
	}
	if w := postJSONTo(vr.create, veh(testID("VEH-RST1"), "RST-1")); w.Code != http.StatusCreated {
		t.Fatalf("first vehicle status %d, want 201; %s", w.Code, w.Body.String())
	}
	w := postJSONTo(vr.create, veh(testID("VEH-RST2"), "RST-2"))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already assigned to another vehicle") {
		t.Fatalf("second vehicle same driver: status %d body %q, want 409", w.Code, w.Body.String())
	}
}
