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

	active := func(id string, completed bool) models.JMP {
		return models.JMP{
			ID: id, DriverID: "DRV-TB", VehicleID: "VEH-TB", Purpose: "x",
			StartDate: "2031-02-01", ExpectedReturn: "2031-02-03", Status: "active",
			Toolbox: models.Toolbox{Completed: completed},
		}
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
	if _, err := repo.JMPs.Add(ctx, models.JMP{
		ID: "JMP-RQ", DriverID: "DRV-RQ", VehicleID: "VEH-OTHER", Purpose: "x",
		StartDate: "2031-03-01", ExpectedReturn: "2031-03-05", Status: "active",
	}); err != nil {
		t.Fatalf("seed jmp: %v", err)
	}
	if _, err := repo.Requests.Add(ctx, models.ServiceRequest{
		ID: "REQ-RQ", RequesterName: "R", RequesterDept: "Ops", Purpose: "x",
		Destination: "Y", StartDate: "2031-03-03", EndDate: "2031-03-04", Status: "approved",
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
