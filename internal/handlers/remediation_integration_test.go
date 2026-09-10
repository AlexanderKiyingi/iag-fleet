package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// HTTP-layer behaviour of the remediation, against a real database.
//
// Handler methods are called directly with a gin test context, the same way the
// existing integration tests do — the permission middleware is attached by
// Register and is not what these are about.
//
// Needs TEST_DATABASE_URL; skips without one.

func remediationVehicle(plate string) models.Vehicle {
	return models.Vehicle{
		ID: uuid.NewString(), Plate: plate, Type: "truck", Make: "Test", Model: "X",
		VehicleClass: "light", Ownership: "Owned", Status: "idle", Location: "Yard",
		Capacity: "1t", LastSeen: time.Now().UTC().Format(time.RFC3339Nano),
		MechStatus: "operational",
	}
}

func patchContext(t *testing.T, id string, body map[string]any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, _ := json.Marshal(body)
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/vehicles/"+id, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: id}}
	return c, w
}

// The failure this exists to stop: setting `status` to completed on a work
// order skipped the parts draw, and completion refuses an already-completed
// row — so the WO was stranded, completed on screen with no stock moved.
func TestIntegration_ServerOwnedFieldRefusedOnPatch(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()

	repo := store.NewRepository(pool)
	ctx := context.Background()

	v, err := repo.Vehicles.Add(ctx, remediationVehicle("ZZ OWNED 1"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := NewVehicleResource(repo, nil)
	c, w := patchContext(t, v.ID, map[string]any{"lifecycleState": "Disposed"})
	res.patch(c)

	if w.Code != http.StatusConflict {
		t.Fatalf("PATCH lifecycleState: status %d, want 409. body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/lifecycle") {
		t.Errorf("the refusal must name the endpoint that owns the field: %s", w.Body.String())
	}

	// And the row must be untouched — a refusal that had already written would
	// be worse than no refusal.
	after, _ := repo.Vehicles.Get(ctx, v.ID)
	if after.LifecycleState == "Disposed" {
		t.Error("the refused PATCH still moved the lifecycle")
	}
}

func TestIntegration_OrdinaryPatchStillWorks(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()

	repo := store.NewRepository(pool)
	ctx := context.Background()

	v, err := repo.Vehicles.Add(ctx, remediationVehicle("ZZ OWNED 2"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := NewVehicleResource(repo, nil)
	c, w := patchContext(t, v.ID, map[string]any{"location": "Gulu"})
	res.patch(c)

	if w.Code != http.StatusOK {
		t.Fatalf("ordinary PATCH: status %d, body %s", w.Code, w.Body.String())
	}
	after, _ := repo.Vehicles.Get(ctx, v.ID)
	if after.Location != "Gulu" {
		t.Errorf("location = %q, want Gulu", after.Location)
	}
}

// The lifecycle endpoint is the only way the field moves, and it has to run the
// state machine rather than take what it is given.
func TestIntegration_VehicleLifecycleTransition(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()

	repo := store.NewRepository(pool)
	ctx := context.Background()

	v, err := repo.Vehicles.Add(ctx, remediationVehicle("ZZ LIFECYCLE HTTP"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	h := &VehicleLifecycle{Repo: repo}

	post := func(body map[string]any) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		raw, _ := json.Marshal(body)
		c.Request = httptest.NewRequest(http.MethodPost,
			"/api/vehicles/"+v.ID+"/lifecycle", bytes.NewReader(raw))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = gin.Params{{Key: "id", Value: v.ID}}
		h.transition(c)
		return w
	}

	// A move that needs a reason must be refused without one.
	if w := post(map[string]any{"to": "Grounded"}); w.Code != http.StatusConflict {
		t.Errorf("Grounded with no reason: status %d, want 409. body: %s", w.Code, w.Body.String())
	}

	// With one, it applies and is persisted.
	if w := post(map[string]any{"to": "Grounded", "reason": "Failed inspection"}); w.Code != http.StatusOK {
		t.Fatalf("Grounded with a reason: status %d, body %s", w.Code, w.Body.String())
	}
	after, _ := repo.Vehicles.Get(ctx, v.ID)
	if after.LifecycleState != "Grounded" {
		t.Fatalf("lifecycleState = %q, want Grounded", after.LifecycleState)
	}
	if after.LifecycleReason != "Failed inspection" {
		t.Errorf("reason = %q", after.LifecycleReason)
	}
	if after.LifecycleAt == "" || after.LifecycleBy == "" {
		t.Errorf("attribution not stamped: at=%q by=%q", after.LifecycleAt, after.LifecycleBy)
	}

	// Grounded → Disposed is not a permitted move; disposal goes through
	// "Held for disposal" first.
	if w := post(map[string]any{"to": "Disposed", "reason": "x", "disposalMethod": "Sale"}); w.Code != http.StatusConflict {
		t.Errorf("Grounded → Disposed: status %d, want 409", w.Code)
	}

	// The real route there, and disposal needs a method.
	if w := post(map[string]any{"to": "Held for disposal", "reason": "Uneconomic"}); w.Code != http.StatusOK {
		t.Fatalf("Held for disposal: %d %s", w.Code, w.Body.String())
	}
	if w := post(map[string]any{"to": "Disposed", "reason": "Sold at auction"}); w.Code != http.StatusConflict {
		t.Errorf("Disposed with no method: status %d, want 409", w.Code)
	}
	if w := post(map[string]any{
		"to": "Disposed", "reason": "Sold at auction", "disposalMethod": "Auction",
		"disposalProceeds": 4500000,
	}); w.Code != http.StatusOK {
		t.Fatalf("Disposed: %d %s", w.Code, w.Body.String())
	}

	disposed, _ := repo.Vehicles.Get(ctx, v.ID)
	if disposed.DisposalMethod != "Auction" {
		t.Errorf("disposalMethod = %q", disposed.DisposalMethod)
	}
	if disposed.DisposalDate == "" {
		t.Error("disposalDate should default to today when not supplied")
	}

	// Terminal: a disposed asset cannot re-enter the fleet.
	if w := post(map[string]any{"to": "Active", "reason": "changed our minds"}); w.Code != http.StatusConflict {
		t.Errorf("Disposed → Active: status %d, want 409", w.Code)
	}
}

// The matrix must stay silent until it is fully configured. Every existing
// deployment has an empty one and drivers.permit_class is free text that many
// rows leave blank, so a rule that denied on missing data would ground the
// fleet the day it shipped.
func TestIntegration_AuthorisationMatrixFailsOpen(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()

	repo := store.NewRepository(pool)
	ctx := context.Background()

	drv, err := repo.Drivers.Add(ctx, models.Driver{
		ID: uuid.NewString(), Name: "ZZ Matrix Driver", Role: "driver",
		Phone: "+256700000001", PermitNo: "ZZ-P1", PermitClass: "CE",
		PermitExpiry: "2033-12-31", Status: "available",
	})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}

	veh, err := repo.Vehicles.Add(ctx, remediationVehicle("ZZ MATRIX 1"))
	if err != nil {
		t.Fatalf("vehicle: %v", err)
	}

	// Gate 1 — no category on the vehicle.
	if err := validateDriverVehicleAuthorisationFor(ctx, repo, drv.ID, veh); err != nil {
		t.Errorf("unclassified vehicle should pass: %v", err)
	}

	cat, err := repo.VehicleCategories.Add(ctx, models.VehicleCategory{
		ID: uuid.NewString(), Name: "ZZ Artic", Code: "ZZ-ART-M", Active: true,
	})
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	veh.CategoryID = cat.ID

	// Gate 2 — the matrix is empty, so nobody has expressed an opinion.
	if err := validateDriverVehicleAuthorisationFor(ctx, repo, drv.ID, veh); err != nil {
		t.Errorf("empty matrix should pass: %v", err)
	}

	// Configure a class the driver does NOT hold, paired with this category.
	otherClass, err := repo.PermitClasses.Add(ctx, models.PermitClass{
		ID: uuid.NewString(), Code: "ZZ-B", Name: "ZZ Light", Active: true,
	})
	if err != nil {
		t.Fatalf("class: %v", err)
	}
	if _, err := repo.PermitAuthorisations.Add(ctx, models.PermitAuthorisation{
		ID: uuid.NewString(), PermitClassID: otherClass.ID, CategoryID: cat.ID,
	}); err != nil {
		t.Fatalf("authorisation: %v", err)
	}

	// Gate 4 — the driver's class "CE" is not a class the operator defined, so
	// there is still nothing to contradict.
	if err := validateDriverVehicleAuthorisationFor(ctx, repo, drv.ID, veh); err != nil {
		t.Errorf("unrecognised licence class should pass: %v", err)
	}

	// Define it. Now every condition holds and the pair is genuinely absent.
	if _, err := repo.PermitClasses.Add(ctx, models.PermitClass{
		ID: uuid.NewString(), Code: "CE", Name: "ZZ Heavy combination", Active: true,
	}); err != nil {
		t.Fatalf("CE class: %v", err)
	}
	err = validateDriverVehicleAuthorisationFor(ctx, repo, drv.ID, veh)
	if err == nil {
		t.Fatal("a fully configured contradiction must be refused")
	}
	if !strings.Contains(err.Error(), "not authorised") {
		t.Errorf("refusal = %v", err)
	}
}

func TestIntegration_AuthorisationMatrixAllowsAConfiguredPair(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()

	repo := store.NewRepository(pool)
	ctx := context.Background()

	cat, _ := repo.VehicleCategories.Add(ctx, models.VehicleCategory{
		ID: uuid.NewString(), Name: "ZZ Rigid", Code: "ZZ-RIG", Active: true,
	})
	class, _ := repo.PermitClasses.Add(ctx, models.PermitClass{
		ID: uuid.NewString(), Code: "ZZ-CE2", Name: "ZZ Heavy", Active: true,
	})
	if _, err := repo.PermitAuthorisations.Add(ctx, models.PermitAuthorisation{
		ID: uuid.NewString(), PermitClassID: class.ID, CategoryID: cat.ID,
	}); err != nil {
		t.Fatalf("authorisation: %v", err)
	}

	drv, _ := repo.Drivers.Add(ctx, models.Driver{
		ID: uuid.NewString(), Name: "ZZ Allowed", Role: "driver",
		Phone: "+256700000002", PermitNo: "ZZ-P2",
		// Deliberately different case and padded: the match is on the licence
		// class a person wrote down, not on an exact string.
		PermitClass: " zz-ce2 ", PermitExpiry: "2033-12-31", Status: "available",
	})

	veh := remediationVehicle("ZZ MATRIX 2")
	veh.CategoryID = cat.ID
	stored, err := repo.Vehicles.Add(ctx, veh)
	if err != nil {
		t.Fatalf("vehicle: %v", err)
	}

	if err := validateDriverVehicleAuthorisationFor(ctx, repo, drv.ID, stored); err != nil {
		t.Errorf("an authorised pair must pass: %v", err)
	}
}

// completeToolbox used to set all eight confirmations regardless of what was
// submitted, because it assumed callers had PATCHed the items first and no
// caller ever did.
func TestIntegration_ToolboxRecordsWhatWasSubmitted(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()

	repo := store.NewRepository(pool)
	ctx := context.Background()

	veh, _ := repo.Vehicles.Add(ctx, remediationVehicle("ZZ TOOLBOX"))
	drv, _ := repo.Drivers.Add(ctx, models.Driver{
		ID: uuid.NewString(), Name: "ZZ TB", Role: "driver", Phone: "+256700000003",
		PermitNo: "ZZ-P3", PermitClass: "CE", PermitExpiry: "2033-12-31", Status: "available",
	})
	jmp, err := repo.JMPs.Add(ctx, models.JMP{
		ID: uuid.NewString(), VehicleID: veh.ID, DriverID: drv.ID, Purpose: "test",
		StartDate: "2026-09-01", ExpectedArrival: "2026-09-02", ExpectedReturn: "2026-09-03",
		Status: "pending-toolbox", MileageStatus: "none",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ParkingPhotos: []string{},
	})
	if err != nil {
		t.Fatalf("jmp: %v", err)
	}

	w := &Workflows{Repo: repo}
	call := func(body map[string]any) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		raw, _ := json.Marshal(body)
		c.Request = httptest.NewRequest(http.MethodPost,
			"/api/jmps/"+jmp.ID+"/complete-toolbox", bytes.NewReader(raw))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = gin.Params{{Key: "id", Value: jmp.ID}}
		w.completeToolbox(c)
		return rec
	}

	// Two of eight ticked: the talk is not complete and must be refused rather
	// than completed on the caller's behalf.
	rec := call(map[string]any{"checks": []string{"drunkDriving", "speedLimits"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("partial toolbox: status %d, want 409. body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cargoInspection") {
		t.Errorf("the refusal should name what is outstanding: %s", rec.Body.String())
	}
	mid, _ := repo.JMPs.Get(ctx, jmp.ID)
	if mid.Toolbox.Completed {
		t.Error("a refused toolbox was still marked complete")
	}

	// All eight, using the ids the UI is built from — four of which differ from
	// the model's own field names.
	rec = call(map[string]any{"checks": []string{
		"drunkDriving", "speedLimits", "cargoInspection", "commsProtocol",
		"fatigue", "incidentContacts", "routeReviewed", "parking",
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("full toolbox: status %d, body %s", rec.Code, rec.Body.String())
	}
	done, _ := repo.JMPs.Get(ctx, jmp.ID)
	if !done.Toolbox.Completed {
		t.Fatal("a complete toolbox was not recorded")
	}
	if done.Status != "active" {
		t.Errorf("status = %q, want active", done.Status)
	}
	if done.Toolbox.Items.ParkingConfirmed == nil || !*done.Toolbox.Items.ParkingConfirmed {
		t.Error("`parking` did not map to ParkingConfirmed — the alias is wrong")
	}
	if done.Toolbox.Items.Communication == nil || !*done.Toolbox.Items.Communication {
		t.Error("`commsProtocol` did not map to Communication — the alias is wrong")
	}
}
