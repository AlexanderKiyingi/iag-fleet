// Integration tests for deployment double-deploy (U2) and driver<->vehicle
// pairing sync (U3). Run with TEST_DATABASE_URL set.
package handlers

import (
	"bytes"
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

func patchCall(handler gin.HandlerFunc, id, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/x/"+id, bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return w
}

// U2: a vehicle/driver may appear at most once in a day's deployment.
func TestIntegration_DeploymentNoDoubleDeploy(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	if _, err := repo.Deployment.Add(ctx, models.DeploymentDay{
		ID: "DPL-1", Date: "2032-04-01", CompiledBy: "t",
		Entries: models.DeploymentEntries{{ID: "DE1", VehicleID: "VEH-DD", DriverID: "DRV-DD"}},
	}); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	w := &Workflows{Repo: repo}
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Params = gin.Params{{Key: "id", Value: "DPL-1"}}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/deployment/DPL-1/entries", bytes.NewReader([]byte(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		w.addDeploymentEntry(c)
		return rec
	}
	if r := post(`{"vehicleId":"VEH-DD"}`); r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "vehicle already") {
		t.Fatalf("dup vehicle: status %d body %q, want 409", r.Code, r.Body.String())
	}
	if r := post(`{"driverId":"DRV-DD"}`); r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "driver already") {
		t.Fatalf("dup driver: status %d body %q, want 409", r.Code, r.Body.String())
	}
	if r := post(`{"vehicleId":"VEH-NEW","driverId":"DRV-NEW"}`); r.Code != http.StatusCreated {
		t.Fatalf("new entry: status %d, want 201; %s", r.Code, r.Body.String())
	}
}

// U3: vehicle.driverId and driver.vehicleId stay mirrored from both sides.
func TestIntegration_DriverVehiclePairingSync(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	if _, err := repo.Drivers.Add(ctx, models.Driver{ID: "DRV-PR", Name: "T", PermitExpiry: "2032-12-31"}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	if _, err := repo.Vehicles.Add(ctx, integrationVehicle("VEH-PR", "PR-1")); err != nil {
		t.Fatalf("seed vehicle: %v", err)
	}
	vr := NewVehicleResource(repo, nil)
	dr := NewDriverResource(repo)

	// Assign driver via the vehicle -> driver.vehicleId follows.
	if r := patchCall(vr.patch, "VEH-PR", `{"driverId":"DRV-PR"}`); r.Code != http.StatusOK {
		t.Fatalf("assign via vehicle: status %d; %s", r.Code, r.Body.String())
	}
	if d, _ := repo.Drivers.Get(ctx, "DRV-PR"); d.VehicleID != "VEH-PR" {
		t.Fatalf("driver.vehicleId = %q, want VEH-PR", d.VehicleID)
	}
	// Detach via the vehicle -> driver.vehicleId cleared.
	if r := patchCall(vr.patch, "VEH-PR", `{"driverId":""}`); r.Code != http.StatusOK {
		t.Fatalf("detach via vehicle: status %d; %s", r.Code, r.Body.String())
	}
	if d, _ := repo.Drivers.Get(ctx, "DRV-PR"); d.VehicleID != "" {
		t.Fatalf("driver.vehicleId = %q after detach, want empty", d.VehicleID)
	}
	// Assign via the driver -> vehicle.driverId follows.
	if r := patchCall(dr.patch, "DRV-PR", `{"vehicleId":"VEH-PR"}`); r.Code != http.StatusOK {
		t.Fatalf("assign via driver: status %d; %s", r.Code, r.Body.String())
	}
	if v, _ := repo.Vehicles.Get(ctx, "VEH-PR"); v.DriverID != "DRV-PR" {
		t.Fatalf("vehicle.driverId = %q, want DRV-PR", v.DriverID)
	}
}
