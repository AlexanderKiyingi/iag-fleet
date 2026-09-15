package store_test

import (
	"context"
	"testing"

	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// TestRepositoryTagsAreWellFormed constructs every Collection.
//
// NewCollection derives the SQL column list reflectively and panics on a
// duplicate `db` tag or a missing `db:"id"`. Both are programmer errors the
// compiler cannot see, and both otherwise surface as a panic during boot in
// whichever environment starts the service next.
//
// A nil pool is fine: NewCollection only stores it, and everything this test
// exercises is reflection over the model type.
func TestRepositoryTagsAreWellFormed(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewRepository panicked — a model has a duplicate or missing db tag: %v", r)
		}
	}()
	if repo := store.NewRepository(nil); repo == nil {
		t.Fatal("NewRepository returned nil")
	}
}

// TestSchemaSpecsCoverEveryCollection guards the list VerifySchema walks.
//
// SchemaSpecs is hand-written, deliberately — the comment on it explains that
// reflection would let a new collection be skipped silently. That reasoning only
// holds if something checks the list is complete, so this is that check: a
// collection added to the repository and forgotten here would be exempt from
// both schema checks without anybody noticing.
func TestSchemaSpecsCoverEveryCollection(t *testing.T) {
	repo := store.NewRepository(nil)
	specs := repo.SchemaSpecs()

	seen := map[string]bool{}
	for _, sp := range specs {
		if seen[sp.Table] {
			t.Errorf("%s appears twice in SchemaSpecs", sp.Table)
		}
		seen[sp.Table] = true
		if len(sp.Columns) == 0 {
			t.Errorf("%s has no columns", sp.Table)
		}
	}

	// Every reflective collection on the repository, by the table it wraps.
	want := []string{
		"vehicles", "drivers", "jmps", "cargo", "cargo_docs",
		"fuel_records", "fuel_requests", "maintenance_items",
		"parts", "tyres", "trips", "safety_events", "compliance_items",
		"service_requests", "task_items", "deployment_days",
		"vehicle_categories", "permit_classes", "permit_authorisations",
		"inspection_templates", "vehicle_inspections", "pm_schedules",
		"weighbridge_tickets", "vehicle_diagnostics", "driver_hos_logs",
		"driver_safety_scores", "fuel_card_reconciliations", "service_reminders",
		"emissions_entries", "route_etas", "carriers",
	}
	for _, table := range want {
		if !seen[table] {
			t.Errorf("SchemaSpecs is missing %s — it is exempt from the schema checks", table)
		}
	}
	if len(specs) != len(want) {
		t.Errorf("SchemaSpecs has %d entries, expected %d — add the new table to `want` too",
			len(specs), len(want))
	}
}

// TestSchemaMatchesModelsInBothDirections is the guard for the failure this
// service has had twice, in both of its forms.
//
//	model → database   a field shipped ahead of its migration. Loud: every read
//	                   of the table 500s with `column "x" does not exist`.
//	database → model   a migration applied and the model never updated. Silent:
//	                   the table works and the column is invisible to the API.
//
// The second is the one that lasted. Migration 0045 added jmps.notes precisely
// to stop journey-plan notes being discarded; the JMP struct never gained the
// field, so they went on being discarded — through a green build, a green test
// suite and a successful deploy.
//
// Needs TEST_DATABASE_URL; skips without one.
func TestSchemaMatchesModelsInBothDirections(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()

	ctx := context.Background()
	specs := store.NewRepository(pool).SchemaSpecs()

	missing, err := store.VerifySchema(ctx, pool, specs)
	if err != nil {
		t.Fatalf("VerifySchema: %v", err)
	}
	if len(missing) > 0 {
		t.Errorf("models select columns the database does not have: %v\n"+
			"a migration has not been applied, or a db tag is misspelled", missing)
	}

	unmapped, err := store.VerifyUnmappedColumns(ctx, pool, specs)
	if err != nil {
		t.Fatalf("VerifyUnmappedColumns: %v", err)
	}
	if len(unmapped) > 0 {
		t.Errorf("database has columns no model reads or writes: %v\n"+
			"that data is invisible to every caller — add the field to the model, "+
			"or record here why it is deliberately unmapped", unmapped)
	}
}
