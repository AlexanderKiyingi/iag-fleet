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

// Pure: a journey may only be 'active' once its toolbox is completed.
func TestRequireToolboxForActive(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		completed bool
		ok        bool
	}{
		{"draft ok", "draft", false, true},
		{"active without toolbox", "active", false, false},
		{"active with toolbox", "active", true, true},
		{"completed ignores toolbox", "completed", false, true},
	}
	for _, tc := range cases {
		j := models.JMP{Status: tc.status, Toolbox: models.Toolbox{Completed: tc.completed}}
		if err := requireToolboxForActive(&j); (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// S1b: creating/activating a JMP via the API without a completed toolbox is 409.
func TestIntegration_JMPActiveRequiresToolbox(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	gin.SetMode(gin.TestMode)
	j := NewJMPs(repo, "")

	// The create handler checks referential integrity once the toolbox gate
	// passes, so the referenced driver/vehicle have to exist for the
	// toolbox-completed case to reach 201.
	ctx := context.Background()
	if _, err := repo.Vehicles.Add(ctx, integrationVehicle("VEH-TB", "TB-1")); err != nil {
		t.Fatalf("seed vehicle: %v", err)
	}
	if _, err := repo.Drivers.Add(ctx, integrationDriver("DRV-TB")); err != nil {
		t.Fatalf("seed driver: %v", err)
	}

	active := func(id string, completed bool) models.JMP {
		j := integrationJMP(id, "VEH-TB", "DRV-TB", "2031-02-01", "2031-02-03", "active")
		j.Toolbox = models.Toolbox{Completed: completed}
		return j
	}
	if w := postJSONTo(j.create, active("JMP-TB1", false)); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "toolbox") {
		t.Fatalf("active-without-toolbox: status %d body %q, want 409 + toolbox", w.Code, w.Body.String())
	}
	if w := postJSONTo(j.create, active("JMP-TB2", true)); w.Code != http.StatusCreated {
		t.Fatalf("active-with-toolbox: status %d, want 201; %s", w.Code, w.Body.String())
	}
}

// S1a: assigning a driver to a request via generic PATCH is subject to the same
// overlap guard as the /assign workflow (can't be bypassed).
func TestIntegration_RequestAssignPatchBlocked(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	if _, err := repo.Drivers.Add(ctx, models.Driver{ID: "DRV-RQ", Name: "T", PermitExpiry: "2031-12-31"}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	// A live journey occupying the driver, NOT sourced from our request.
	if _, err := repo.JMPs.Add(ctx, integrationJMP("JMP-RQ", "VEH-OTHER", "DRV-RQ", "2031-03-01", "2031-03-05", "active")); err != nil {
		t.Fatalf("seed jmp: %v", err)
	}
	if _, err := repo.Requests.Add(ctx, models.ServiceRequest{
		ID: "REQ-RQ", RequesterName: "R", RequesterDept: "Ops", Purpose: "x",
		Destination: "Y", StartDate: "2031-03-03", EndDate: "2031-03-04", Status: "approved",
		SubmittedAt: "2031-03-01T08:00:00Z",
	}); err != nil {
		t.Fatalf("seed request: %v", err)
	}

	rr := NewRequestResource(repo, nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "REQ-RQ"}}
	body := []byte(`{"assignedDriverId":"DRV-RQ","assignedVehicleId":"VEH-RQ"}`)
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/requests/REQ-RQ", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	rr.patch(c)

	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "driver already") {
		t.Fatalf("PATCH-assign overlapping driver: status %d body %q, want 409", w.Code, w.Body.String())
	}
}
