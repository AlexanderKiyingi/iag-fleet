package store

import (
	"context"
	"time"
)

// ComplianceExpiringWithinDays is the window before expiry that maps to status=expiring.
const ComplianceExpiringWithinDays = 14

// ComplianceStatusFromExpiry derives compliance status from an expiry date (YYYY-MM-DD).
func ComplianceStatusFromExpiry(expiry string, today time.Time, expiringWithinDays int) string {
	if expiry == "" {
		return "missing"
	}
	d, err := time.Parse("2006-01-02", expiry)
	if err != nil {
		return "missing"
	}
	today = today.UTC().Truncate(24 * time.Hour)
	d = d.UTC().Truncate(24 * time.Hour)
	days := int(d.Sub(today).Hours() / 24)
	if days < 0 {
		return "expired"
	}
	if days <= expiringWithinDays {
		return "expiring"
	}
	return "valid"
}

// RecomputeComplianceStatuses updates compliance_items.status from expiry dates.
// Returns the number of rows whose status changed.
//
// One set-based UPDATE, not a read of the whole table followed by a separate
// read-modify-write transaction per drifted row. On a day when many documents
// roll over at once — which is the normal case, since renewals cluster — the
// old shape issued hundreds of sequential transactions and took minutes.
//
// The CASE below is ComplianceStatusFromExpiry expressed in SQL, and
// TestComplianceStatusSQLMatchesGo pins the two together: a NULL or unparseable
// expiry is "missing", a past expiry is "expired", one inside the window is
// "expiring", anything further out is "valid". The `IS DISTINCT FROM` guard
// keeps the write to rows that actually changed, so an idempotent re-run costs
// one scan and no writes.
func (r *Repository) RecomputeComplianceStatuses(ctx context.Context) (int, error) {
	const q = `
        UPDATE compliance_items SET status = derived.want
        FROM (
            SELECT id,
                   CASE
                       WHEN expiry IS NULL THEN 'missing'
                       WHEN expiry < (NOW() AT TIME ZONE 'UTC')::date THEN 'expired'
                       WHEN expiry <= (NOW() AT TIME ZONE 'UTC')::date + ($1::int * INTERVAL '1 day')
                            THEN 'expiring'
                       ELSE 'valid'
                   END AS want
            FROM compliance_items
        ) AS derived
        WHERE compliance_items.id = derived.id
          AND compliance_items.status IS DISTINCT FROM derived.want`
	tag, err := r.pool.Exec(ctx, q, ComplianceExpiringWithinDays)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
