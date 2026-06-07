package jobs

import (
	"context"

	"github.com/iag/fleet-tool/backend/internal/store"
)

// RecomputeComplianceStatuses derives compliance_items.status from expiry dates.
func RecomputeComplianceStatuses(ctx context.Context, repo *store.Repository) (int, error) {
	return repo.RecomputeComplianceStatuses(ctx)
}
