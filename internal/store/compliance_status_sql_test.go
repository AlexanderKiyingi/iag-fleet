package store

import (
	"context"
	"testing"
	"time"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// RecomputeComplianceStatuses derives status in SQL now, so the rule exists in
// two places: ComplianceStatusFromExpiry (read by the notifications producer
// and the seed) and the CASE expression in the UPDATE. They must agree — a
// drift between them would show as a dashboard counter that disagrees with the
// compliance page, with nothing failing.
//
// This runs the same expiry dates through both and compares. It skips without
// TEST_DATABASE_URL, like the rest of the integration suite.
func TestIntegration_ComplianceStatusSQLMatchesGo(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()
	ctx := context.Background()
	repo := NewRepository(pool)

	today := time.Now().UTC()
	day := func(offset int) string {
		return today.AddDate(0, 0, offset).Format("2006-01-02")
	}

	// One case either side of every boundary the rule has: past, today,
	// inside the expiring window, exactly on it, just past it, and absent.
	cases := []struct {
		id     string
		expiry string
	}{
		{"CI-SQL-PAST", day(-30)},
		{"CI-SQL-YESTERDAY", day(-1)},
		{"CI-SQL-TODAY", day(0)},
		{"CI-SQL-INSIDE", day(ComplianceExpiringWithinDays - 1)},
		{"CI-SQL-BOUNDARY", day(ComplianceExpiringWithinDays)},
		{"CI-SQL-OUTSIDE", day(ComplianceExpiringWithinDays + 1)},
		{"CI-SQL-FAR", day(365)},
		{"CI-SQL-MISSING", ""},
	}

	for _, tc := range cases {
		// Seed with a deliberately wrong status so every row must be rewritten
		// — that also exercises the IS DISTINCT FROM guard's positive branch.
		if _, err := repo.Compliance.Add(ctx, models.ComplianceItem{
			ID: tc.id, DocType: "test-doc", Expiry: tc.expiry, Status: "unset",
		}); err != nil {
			t.Fatalf("seed %s: %v", tc.id, err)
		}
		t.Cleanup(func() { _ = repo.Compliance.Delete(ctx, tc.id) })
	}

	if _, err := repo.RecomputeComplianceStatuses(ctx); err != nil {
		t.Fatalf("RecomputeComplianceStatuses: %v", err)
	}

	for _, tc := range cases {
		got, err := repo.Compliance.Get(ctx, tc.id)
		if err != nil {
			t.Fatalf("Get %s: %v", tc.id, err)
		}
		want := ComplianceStatusFromExpiry(tc.expiry, today, ComplianceExpiringWithinDays)
		if got.Status != want {
			t.Errorf("expiry %q (%s): SQL produced %q, ComplianceStatusFromExpiry says %q",
				tc.expiry, tc.id, got.Status, want)
		}
	}

	// Idempotence: a second run must report zero changed rows, or the job is
	// rewriting the whole table on every tick.
	n, err := repo.RecomputeComplianceStatuses(ctx)
	if err != nil {
		t.Fatalf("second RecomputeComplianceStatuses: %v", err)
	}
	if n != 0 {
		t.Errorf("re-run changed %d rows, want 0 — the guard is not holding", n)
	}
}
