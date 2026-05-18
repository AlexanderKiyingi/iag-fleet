package cache

import "context"

// Redis key namespace — single-tenant fleet DB; all users share aggregates.
const (
	keyPrefix         = "haula:v1:"
	KeyDashboard      = keyPrefix + "dashboard:summary"
	KeyAnalytics      = keyPrefix + "analytics:summary"
	KeyReferenceAll   = keyPrefix + "reference:all"
	KeyReferenceGeo   = keyPrefix + "reference:geo"
)

// InvalidateFleetAggregates drops cached dashboard, analytics, and reference
// JSON. Call after bulk data mutations (admin import/reset).
func InvalidateFleetAggregates(ctx context.Context, c Cache) error {
	if c == nil {
		return nil
	}
	return c.Delete(ctx, KeyDashboard, KeyAnalytics, KeyReferenceAll, KeyReferenceGeo)
}
