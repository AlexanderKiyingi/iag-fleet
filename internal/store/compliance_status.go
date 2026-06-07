package store

import (
	"context"
	"time"

	"github.com/iag/fleet-tool/backend/internal/models"
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
func (r *Repository) RecomputeComplianceStatuses(ctx context.Context) (int, error) {
	items, err := r.Compliance.List(ctx)
	if err != nil {
		return 0, err
	}
	today := time.Now().UTC()
	updated := 0
	for _, item := range items {
		want := ComplianceStatusFromExpiry(item.Expiry, today, ComplianceExpiringWithinDays)
		if item.Status == want {
			continue
		}
		id := item.ID
		if _, err := r.Compliance.Update(ctx, id, func(ci *models.ComplianceItem) {
			ci.Status = want
		}); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}
