package store

import (
	"context"
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// Collection.Update committed while the UPDATE … RETURNING rows were still
// open (they were only closed by a deferred call that ran after COMMIT). An
// open pgx Rows owns the connection, so every call failed with "conn busy" —
// which meant every generic PATCH endpoint returned 500. Nothing caught it
// because the integration suite skips unless TEST_DATABASE_URL is set.
func TestIntegration_CollectionUpdateCommits(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()
	ctx := context.Background()
	repo := NewRepository(pool)

	if _, err := repo.Parts.Add(ctx, models.Part{
		ID: "PT-UPD", Name: "Filter", Category: "Filters", SKU: "UPD-1", Stock: 5,
	}); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	updated, err := repo.Parts.Update(ctx, "PT-UPD", func(p *models.Part) {
		p.Stock = 2
		p.Name = "Filter (renamed)"
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Stock != 2 || updated.Name != "Filter (renamed)" {
		t.Fatalf("returned row = {stock:%d name:%q}, want {2 \"Filter (renamed)\"}", updated.Stock, updated.Name)
	}

	// The mutation must actually be committed, not just reflected in the
	// RETURNING row of a transaction that never landed.
	got, err := repo.Parts.Get(ctx, "PT-UPD")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Stock != 2 || got.Name != "Filter (renamed)" {
		t.Fatalf("persisted row = {stock:%d name:%q}, want the update to have committed", got.Stock, got.Name)
	}

	// The connection must be reusable straight afterwards; a leaked Rows would
	// surface here rather than in the call that caused it.
	if _, err := repo.Parts.Update(ctx, "PT-UPD", func(p *models.Part) { p.Stock = 1 }); err != nil {
		t.Fatalf("second consecutive Update: %v", err)
	}
}

func TestIntegration_CollectionUpdateMissingRow(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()
	ctx := context.Background()
	repo := NewRepository(pool)

	if _, err := repo.Parts.Update(ctx, "PT-NOPE", func(p *models.Part) { p.Stock = 1 }); err != ErrNotFound {
		t.Fatalf("Update on a missing row = %v, want ErrNotFound", err)
	}

	// The failed lookup must not strand the connection either.
	if _, err := repo.Parts.Add(ctx, models.Part{
		ID: "PT-AFTER", Name: "F", Category: "C", SKU: "A-1", Stock: 1,
	}); err != nil {
		t.Fatalf("Add after a not-found Update: %v", err)
	}
}
