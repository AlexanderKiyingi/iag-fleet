package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/config"
	"github.com/iag/fleet-tool/backend/internal/procurementclient"
)

// Fleet's approval authorizes the vehicle, driver and litres. It does not
// authorize the spend — procurement imports an approved fuel request as a
// sourcing requisition and walks it through PM -> Accounts Assistant -> GM
// (>= 5,000,000) -> CEO (>= 20,000,000), encumbering the budget. Before this
// gate, fulfil checked only `status != "approved"`, so anyone holding
// add_fuel_record could commit any amount without those desks signing.
//
// The gate is on AUTHORIZATION, never payment: fuel is routinely bought on
// credit, and procurement migration 022 removed the money desk from that chain
// precisely because it disburses nothing.

type noopTokens struct{}

func (noopTokens) AuthorizeRequest(_ context.Context, _ *http.Request) error { return nil }

// procurementStub serves the by-origin lookup with a fixed status, or the given
// HTTP status when body is empty.
func procurementStub(t *testing.T, code int, status string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status == "" {
			w.WriteHeader(code)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "PR-2026-0007", "status": status, "total": 8_000_000, "currency": "UGX",
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func gateFor(t *testing.T, srv *httptest.Server, cfg config.Config) (*FuelRequests, *gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var client *procurementclient.Client
	if srv != nil {
		client = procurementclient.New(procurementclient.Options{
			BaseURL: srv.URL, Tokens: noopTokens{},
		})
	}
	f := &FuelRequests{procurement: client, cfg: cfg}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/fuel-requests/FREQ-1/fulfill", nil)
	return f, c, rec
}

func TestGateOffAllowsFulfilment(t *testing.T) {
	// Turning on the read-only procurement bridge must not start refusing
	// fulfilments as a side effect. The gate is its own switch.
	srv := procurementStub(t, 200, "GM Approved")
	f, c, _ := gateFor(t, srv, config.Config{FuelAuthorizationGateEnabled: false})

	if !f.commitmentAuthorized(c, "FREQ-1") {
		t.Error("gate disabled must allow fulfilment regardless of procurement state")
	}
}

func TestUnauthorizedCommitmentIsRefused(t *testing.T) {
	// The whole point: a requisition still sitting on a desk has not had the
	// bands above it sign, so the fuel must not be released.
	for _, desk := range []string{
		"Submitted", "PM Approved", "Accounts Assistant Approved",
		"GM Approved", "CEO Approved",
	} {
		srv := procurementStub(t, 200, desk)
		f, c, rec := gateFor(t, srv, config.Config{
			FuelAuthorizationGateEnabled: true,
			FuelAuthorizationFailOpen:    true,
		})
		if f.commitmentAuthorized(c, "FREQ-1") {
			t.Errorf("%q is not an authorized commitment; fulfilment must be refused", desk)
		}
		if rec.Code != http.StatusConflict {
			t.Errorf("%q: status = %d, want 409", desk, rec.Code)
		}
	}
}

func TestAuthorizedCommitmentIsAllowed(t *testing.T) {
	// "Approved for Procurement" is the live chain terminal (migration 022).
	// The other two are terminals of earlier revisions of the same chain and
	// must keep working, so a requisition raised before those migrations is not
	// stranded. None of them ever meant cash had moved.
	for _, status := range []string{
		"Approved for Procurement", "Approved", "Payment Authorized", "Paid",
	} {
		srv := procurementStub(t, 200, status)
		f, c, _ := gateFor(t, srv, config.Config{
			FuelAuthorizationGateEnabled: true,
			FuelAuthorizationFailOpen:    true,
		})
		if !f.commitmentAuthorized(c, "FREQ-1") {
			t.Errorf("%q is an authorized commitment; fulfilment must proceed", status)
		}
	}
}

func TestNoRequisitionFallsBackToFleetApproval(t *testing.T) {
	// The bridge may not have processed the approval event yet, or the request
	// predates the integration. There is no chain to wait for.
	srv := procurementStub(t, http.StatusNotFound, "")
	f, c, _ := gateFor(t, srv, config.Config{
		FuelAuthorizationGateEnabled: true,
		FuelAuthorizationFailOpen:    false,
	})
	if !f.commitmentAuthorized(c, "FREQ-1") {
		t.Error("no requisition means no chain to wait for; fleet approval stands")
	}
}

func TestProcurementOutageFailsOpenByDefault(t *testing.T) {
	// A procurement outage must not stop a fleet taking fuel. A stranded truck
	// is a worse and more immediate failure than a band bypass the audit trail
	// still records.
	srv := procurementStub(t, http.StatusInternalServerError, "")
	f, c, _ := gateFor(t, srv, config.Config{
		FuelAuthorizationGateEnabled: true,
		FuelAuthorizationFailOpen:    true,
	})
	if !f.commitmentAuthorized(c, "FREQ-1") {
		t.Error("fail-open must allow fulfilment when procurement is unreachable")
	}
}

func TestProcurementOutageCanFailClosed(t *testing.T) {
	// Deployments that would rather stop the fleet than risk an unauthorized
	// commitment can say so.
	srv := procurementStub(t, http.StatusInternalServerError, "")
	f, c, rec := gateFor(t, srv, config.Config{
		FuelAuthorizationGateEnabled: true,
		FuelAuthorizationFailOpen:    false,
	})
	if f.commitmentAuthorized(c, "FREQ-1") {
		t.Error("fail-closed must refuse when procurement cannot be reached")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestGateInertWithoutProcurementClient(t *testing.T) {
	// Gate on, integration off: nothing to ask, so nothing to refuse.
	f, c, _ := gateFor(t, nil, config.Config{FuelAuthorizationGateEnabled: true})
	if !f.commitmentAuthorized(c, "FREQ-1") {
		t.Error("gate must be inert when the procurement integration is disabled")
	}
}

// The default must be fail-open: only an explicit "false" flips it.
func TestFailOpenDefault(t *testing.T) {
	// Load validates the whole environment; supply the one required value.
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/fleet?sslmode=disable")
	t.Setenv("FUEL_AUTHORIZATION_FAIL_OPEN", "")
	got, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !got.FuelAuthorizationFailOpen {
		t.Error("unset FUEL_AUTHORIZATION_FAIL_OPEN must default to fail-open")
	}

	t.Setenv("FUEL_AUTHORIZATION_FAIL_OPEN", "false")
	got, err = config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got.FuelAuthorizationFailOpen {
		t.Error("FUEL_AUTHORIZATION_FAIL_OPEN=false must fail closed")
	}
}
