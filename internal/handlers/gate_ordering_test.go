// Integration tests for SOFT status-ordering gates (config.GateOrderingEnabled).
// Run with a test DB, e.g.:
//
//	TEST_DATABASE_URL=postgres://svc_iag_fleet:iag_fleet_dev@localhost:5432/iag_platform?sslmode=disable \
//	  go test ./internal/handlers/... -run Integration_GateOrder -v
package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/iag/fleet-tool/backend/internal/config"
	"github.com/iag/fleet-tool/backend/internal/ctxkeys"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// postParamTo invokes a handler with a single :id path param and an empty JSON
// body. When claims is non-nil it is placed on the context so auth.HasPerm can
// resolve permissions (e.g. a superuser, which holds the gate-override perm).
func postParamTo(handler gin.HandlerFunc, id string, claims *authclient.Claims) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: id}}
	if claims != nil {
		c.Set(ctxkeys.Claims, claims)
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("{}")))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return w
}

func seedAssignedRequest(t *testing.T, repo *store.Repository, id string, approved, assignmentApproved bool) {
	t.Helper()
	r := models.ServiceRequest{
		ID: id, RequesterName: "R", RequesterDept: "Ops", Purpose: "x",
		Destination: "Y", StartDate: "2031-05-01", EndDate: "2031-05-02", Status: "assigned",
		AssignedVehicleID: "VEH-" + id, AssignedDriverID: "DRV-" + id,
	}
	if approved {
		r.ApprovedBy, r.ApprovedAt = "approver", "2031-04-30T08:00:00Z"
	}
	if assignmentApproved {
		r.AssignmentApprovedBy, r.AssignmentApprovedAt = "approver", "2031-04-30T09:00:00Z"
	}
	if _, err := repo.Requests.Add(context.Background(), r); err != nil {
		t.Fatalf("seed request %s: %v", id, err)
	}
}

// Deploy is blocked until the request is approved AND its assignment signed off;
// a principal holding the override perm (superuser here) may still proceed.
func TestIntegration_GateOrderDeployRequiresApproval(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	gin.SetMode(gin.TestMode)
	w := &Workflows{Repo: repo, Config: config.Config{GateOrderingEnabled: true}}

	seedAssignedRequest(t, repo, "REQ-GATE-DEP", false, false)

	// Out of order, no override → 409.
	if rr := postParamTo(w.deployRequest, "REQ-GATE-DEP", nil); rr.Code != http.StatusConflict ||
		!strings.Contains(rr.Body.String(), "approved") {
		t.Fatalf("deploy before approval: status %d body %q, want 409 + approved", rr.Code, rr.Body.String())
	}

	// Override (superuser holds gate-override) → proceeds.
	if rr := postParamTo(w.deployRequest, "REQ-GATE-DEP", &authclient.Claims{IsSuperuser: true}); rr.Code != http.StatusOK {
		t.Fatalf("deploy with override: status %d, want 200; %s", rr.Code, rr.Body.String())
	}
}

// With the flag off, the dispatch chain stays independent — deploy succeeds even
// though the request was never approved.
func TestIntegration_GateOrderDisabledAllowsOutOfOrder(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	gin.SetMode(gin.TestMode)
	w := &Workflows{Repo: repo, Config: config.Config{GateOrderingEnabled: false}}

	seedAssignedRequest(t, repo, "REQ-GATE-OFF", false, false)

	if rr := postParamTo(w.deployRequest, "REQ-GATE-OFF", nil); rr.Code != http.StatusOK {
		t.Fatalf("deploy with gate off: status %d, want 200; %s", rr.Code, rr.Body.String())
	}
}

// A JMP whose dispatch was rejected cannot be completed while the gate is on.
func TestIntegration_GateOrderJMPCompleteBlockedOnRejectedDispatch(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)
	w := &Workflows{Repo: repo, Config: config.Config{GateOrderingEnabled: true}}

	if _, err := repo.JMPs.Add(ctx, models.JMP{
		ID: "JMP-GATE", DriverID: "DRV-G", VehicleID: "VEH-G", Purpose: "x",
		StartDate: "2031-06-01", ExpectedReturn: "2031-06-03", Status: "active",
		Toolbox: models.Toolbox{Completed: true}, DispatchStatus: "Rejected",
	}); err != nil {
		t.Fatalf("seed jmp: %v", err)
	}

	if rr := postParamTo(w.completeJmp, "JMP-GATE", nil); rr.Code != http.StatusConflict ||
		!strings.Contains(rr.Body.String(), "dispatch") {
		t.Fatalf("complete with rejected dispatch: status %d body %q, want 409 + dispatch", rr.Code, rr.Body.String())
	}

	// Once dispatch is approved, completion proceeds.
	if _, err := repo.JMPs.Update(ctx, "JMP-GATE", func(j *models.JMP) { j.DispatchStatus = "Approved" }); err != nil {
		t.Fatalf("approve dispatch: %v", err)
	}
	if rr := postParamTo(w.completeJmp, "JMP-GATE", nil); rr.Code != http.StatusOK {
		t.Fatalf("complete with approved dispatch: status %d, want 200; %s", rr.Code, rr.Body.String())
	}
}
