package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// The store-level behaviour of migrations 0048–0050, against a real database.
//
// These are the parts that cannot be proved by a build or a unit test: whether
// the columns exist, whether the trigger actually fires, and whether the values
// survive the round trip through the reflective column mapping. Everything here
// was written from reading SQL and Go, and reading is what produced the four
// wrong adapter mappings the cutover doc records.
//
// Needs TEST_DATABASE_URL; skips without one.

// Ids are minted here because Collection.Add does not: the HTTP layer generates
// them (handlers.generateID) and the store takes what it is given. Migration
// 0043 retyped every id column to uuid, so it has to be one.
func newVehicle(plate string) models.Vehicle {
	return models.Vehicle{
		ID:       uuid.NewString(),
		Plate:    plate,
		Type:     "Truck",
		Status:   "idle",
		Location: "Kampala",
		LastSeen: time.Now().UTC().Format(time.RFC3339),
	}
}

func TestIntegration_VehicleLifecycleColumnsRoundTrip(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()

	ctx := context.Background()
	repo := store.NewRepository(pool)

	proceeds := 4_500_000.0
	v := newVehicle("ZZ LIFECYCLE 1")
	v.LifecycleState = "Held for disposal"
	v.LifecycleReason = "Uneconomic to repair"
	v.LifecycleBy = "ops@example.com"
	v.LifecycleAt = time.Now().UTC().Format(time.RFC3339)
	v.DisposalMethod = "Auction"
	v.DisposalDate = "2026-09-01"
	v.DisposalProceeds = &proceeds
	v.DisposalBuyer = "Kampala Auctions"

	created, err := repo.Vehicles.Add(ctx, v)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	read, err := repo.Vehicles.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Before 0048 every one of these was dropped by json.Unmarshal and the API
	// answered 200 with the row unchanged.
	if read.LifecycleState != "Held for disposal" {
		t.Errorf("lifecycleState = %q, want %q", read.LifecycleState, "Held for disposal")
	}
	if read.LifecycleReason != "Uneconomic to repair" {
		t.Errorf("lifecycleReason = %q", read.LifecycleReason)
	}
	if read.LifecycleBy != "ops@example.com" {
		t.Errorf("lifecycleBy = %q", read.LifecycleBy)
	}
	if read.DisposalMethod != "Auction" || read.DisposalBuyer != "Kampala Auctions" {
		t.Errorf("disposal method/buyer = %q / %q", read.DisposalMethod, read.DisposalBuyer)
	}
	if read.DisposalDate != "2026-09-01" {
		t.Errorf("disposalDate = %q, want 2026-09-01", read.DisposalDate)
	}
	if read.DisposalProceeds == nil || *read.DisposalProceeds != proceeds {
		t.Errorf("disposalProceeds = %v, want %v", read.DisposalProceeds, proceeds)
	}
}

// A vehicle with no disposal recorded must read back as nil, not 0. The two
// mean different things — scrapped for nothing is a real outcome — which is why
// the column is nullable and the field a pointer.
func TestIntegration_DisposalProceedsDistinguishesUnsetFromZero(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()

	ctx := context.Background()
	repo := store.NewRepository(pool)

	unset, err := repo.Vehicles.Add(ctx, newVehicle("ZZ PROCEEDS UNSET"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	readUnset, _ := repo.Vehicles.Get(ctx, unset.ID)
	if readUnset.DisposalProceeds != nil {
		t.Errorf("unset proceeds read back as %v, want nil", *readUnset.DisposalProceeds)
	}

	zero := 0.0
	v := newVehicle("ZZ PROCEEDS ZERO")
	v.DisposalProceeds = &zero
	created, err := repo.Vehicles.Add(ctx, v)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	readZero, _ := repo.Vehicles.Get(ctx, created.ID)
	if readZero.DisposalProceeds == nil {
		t.Fatal("a recorded zero read back as nil — scrapped-for-nothing is not the same as unrecorded")
	}
	if *readZero.DisposalProceeds != 0 {
		t.Errorf("proceeds = %v, want 0", *readZero.DisposalProceeds)
	}
}

// jmps.notes shipped in migration 0045 and the struct never gained the field,
// so the column list — derived from those tags — neither selected nor wrote it.
func TestIntegration_JMPNotesRoundTrip(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()

	ctx := context.Background()
	repo := store.NewRepository(pool)

	veh, err := repo.Vehicles.Add(ctx, newVehicle("ZZ JMP NOTES"))
	if err != nil {
		t.Fatalf("vehicle: %v", err)
	}
	drv, err := repo.Drivers.Add(ctx, models.Driver{
		ID: uuid.NewString(), Name: "ZZ Driver", Role: "driver",
		Phone: "0700000000", Status: "active",
		// drivers.permit_no / permit_expiry are NOT NULL since 0001.
		PermitNo: "ZZ-PERMIT-1", PermitExpiry: "2030-01-01",
	})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}

	created, err := repo.JMPs.Add(ctx, models.JMP{
		ID:        uuid.NewString(),
		VehicleID: veh.ID, DriverID: drv.ID,
		Purpose: "Cargo run", StartDate: "2026-09-01", ExpectedArrival: "2026-09-02",
		// jmps has several NOT NULL date columns from 0001; the HTTP layer fills
		// them through jmp.Enrich, which this store-level test bypasses.
		ExpectedReturn: "2026-09-03",
		Status:         "draft",
		Notes:          "Convoy with UAH 456Y; overnight at Gulu yard.",
	})
	if err != nil {
		t.Fatalf("add jmp: %v", err)
	}
	read, err := repo.JMPs.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get jmp: %v", err)
	}
	if !strings.Contains(read.Notes, "Convoy with UAH 456Y") {
		t.Errorf("notes = %q — the column exists since 0045 and was still being discarded", read.Notes)
	}
}

// The whole point of 0050: updated_at has to move on every write, or the
// client's collection revision degrades to a row count and concurrent edits to
// different rows stop conflicting.
func TestIntegration_TouchRowTriggerMovesUpdatedAt(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()

	ctx := context.Background()
	repo := store.NewRepository(pool)

	created, err := repo.Vehicles.Add(ctx, newVehicle("ZZ TOUCH 1"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if created.CreatedAt == "" || created.UpdatedAt == "" {
		t.Fatalf("timestamps empty on insert: created=%q updated=%q", created.CreatedAt, created.UpdatedAt)
	}

	// Postgres NOW() is transaction time, so two writes in the same instant
	// would tie. A real gap is what the revision check needs to see.
	time.Sleep(1100 * time.Millisecond)

	updated, err := repo.Vehicles.Update(ctx, created.ID, func(v *models.Vehicle) {
		v.Location = "Gulu"
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.UpdatedAt == created.UpdatedAt {
		t.Errorf("updated_at did not move on write (%q) — the trigger is not firing", updated.UpdatedAt)
	}
	// created_at is immutable by construction: Replace names every column, so
	// without the trigger's OLD.created_at a full write would reset or null it.
	if updated.CreatedAt != created.CreatedAt {
		t.Errorf("created_at changed on update: %q -> %q", created.CreatedAt, updated.CreatedAt)
	}
}

// A caller sending its own updated_at must not be able to freeze it. The
// trigger fires after the statement's SET list, which is what makes the column
// authoritative rather than advisory.
func TestIntegration_TouchRowTriggerOverridesACallerSuppliedStamp(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()

	ctx := context.Background()
	repo := store.NewRepository(pool)

	created, err := repo.Vehicles.Add(ctx, newVehicle("ZZ TOUCH 2"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	stale := "2020-01-01T00:00:00Z"
	updated, err := repo.Vehicles.Update(ctx, created.ID, func(v *models.Vehicle) {
		v.UpdatedAt = stale
		v.CreatedAt = stale
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if strings.HasPrefix(updated.UpdatedAt, "2020") {
		t.Errorf("caller-supplied updated_at was accepted (%q)", updated.UpdatedAt)
	}
	if strings.HasPrefix(updated.CreatedAt, "2020") {
		t.Errorf("caller-supplied created_at was accepted (%q)", updated.CreatedAt)
	}
}

// The three taxonomy tables and the vehicles.category_id that references them —
// the frontend has had complete read-write screens for these against endpoints
// that existed on no branch.
func TestIntegration_TaxonomyAndVehicleCategory(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()

	ctx := context.Background()
	repo := store.NewRepository(pool)

	cat, err := repo.VehicleCategories.Add(ctx, models.VehicleCategory{
		ID: uuid.NewString(), Name: "ZZ Articulated", Code: "ZZ-ART", Active: true,
	})
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	class, err := repo.PermitClasses.Add(ctx, models.PermitClass{
		ID: uuid.NewString(), Code: "ZZ-CE", Name: "ZZ Heavy combination", Active: true,
	})
	if err != nil {
		t.Fatalf("class: %v", err)
	}
	auth, err := repo.PermitAuthorisations.Add(ctx, models.PermitAuthorisation{
		ID:            uuid.NewString(),
		PermitClassID: class.ID, CategoryID: cat.ID, Notes: "ZZ test pair",
	})
	if err != nil {
		t.Fatalf("authorisation: %v", err)
	}
	if auth.PermitClassID != class.ID || auth.CategoryID != cat.ID {
		t.Errorf("authorisation lost its references: %+v", auth)
	}

	v := newVehicle("ZZ CATEGORY 1")
	v.CategoryID = cat.ID
	created, err := repo.Vehicles.Add(ctx, v)
	if err != nil {
		t.Fatalf("vehicle with category: %v", err)
	}
	read, _ := repo.Vehicles.Get(ctx, created.ID)
	if read.CategoryID != cat.ID {
		t.Errorf("categoryId = %q, want %q", read.CategoryID, cat.ID)
	}

	// ON DELETE SET NULL: retiring a category must not be blocked by the fleet
	// still referencing it, and an unclassified vehicle is a state the dispatch
	// rule already handles by allowing.
	if err := repo.VehicleCategories.Delete(ctx, cat.ID); err != nil {
		t.Fatalf("delete category: %v", err)
	}
	afterDelete, _ := repo.Vehicles.Get(ctx, created.ID)
	if afterDelete.CategoryID != "" {
		t.Errorf("categoryId = %q after the category was deleted, want empty", afterDelete.CategoryID)
	}
}

// Found by TestSchemaMatchesModelsInBothDirections: the column has been read by
// the overspeed detector since 0036 and nothing could write it, so it was NULL
// on every row and every vehicle fell back to the global limit.
func TestIntegration_SpeedLimitRoundTrip(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()

	ctx := context.Background()
	repo := store.NewRepository(pool)

	limit := 80.0
	v := newVehicle("ZZ SPEED 1")
	v.SpeedLimitKmh = &limit
	created, err := repo.Vehicles.Add(ctx, v)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	read, _ := repo.Vehicles.Get(ctx, created.ID)
	if read.SpeedLimitKmh == nil || *read.SpeedLimitKmh != limit {
		t.Fatalf("speedLimitKmh = %v, want %v", read.SpeedLimitKmh, limit)
	}

	// The column's three states all mean different things: NULL falls back to
	// the fleet default, 0 disables monitoring for this vehicle, a value is the
	// limit. Collapsing NULL and 0 would switch monitoring off everywhere.
	off := 0.0
	disabled, err := repo.Vehicles.Update(ctx, created.ID, func(v *models.Vehicle) {
		v.SpeedLimitKmh = &off
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if disabled.SpeedLimitKmh == nil {
		t.Fatal("an explicit 0 read back as nil — 'monitoring off' became 'use the default'")
	}
	if *disabled.SpeedLimitKmh != 0 {
		t.Errorf("speedLimitKmh = %v, want 0", *disabled.SpeedLimitKmh)
	}
}
