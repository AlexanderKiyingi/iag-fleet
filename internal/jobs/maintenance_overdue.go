package jobs

import (
	"context"
	"time"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// MarkOverdueMaintenance sets status=overdue on scheduled work orders whose date is before today.
func MarkOverdueMaintenance(ctx context.Context, repo *store.Repository) (int, error) {
	items, err := repo.Maintenance.List(ctx)
	if err != nil {
		return 0, err
	}
	today := time.Now().UTC().Format("2006-01-02")
	updated := 0
	for _, mx := range items {
		if mx.Status != "scheduled" || mx.Date == "" {
			continue
		}
		if mx.Date >= today {
			continue
		}
		id := mx.ID
		if _, err := repo.Maintenance.Update(ctx, id, func(m *models.MaintenanceItem) {
			m.Status = "overdue"
		}); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}
