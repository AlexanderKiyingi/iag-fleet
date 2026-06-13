package handlers

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// NewDriverResource returns driver CRUD with validation and compliance sync.
func NewDriverResource(repo *store.Repository) *Resource[models.Driver, *models.Driver] {
	r := &Resource[models.Driver, *models.Driver]{
		Repo:       repo,
		Collection: repo.Drivers,
		Entity:     "driver",
		IDPrefix:   "DRV",
	}
	r.BeforeCreate = func(c *gin.Context, item *models.Driver) error {
		return validateDriver(item)
	}
	r.BeforeUpdate = func(c *gin.Context, item *models.Driver) error {
		return validateDriver(item)
	}
	r.BeforeDelete = func(ctx context.Context, id string) error {
		return validateDriverDeletable(ctx, repo, id)
	}
	r.AfterCreate = func(ctx context.Context, item models.Driver) {
		syncDriverVehiclePairing(ctx, repo, item)
		_ = repo.SyncDriverComplianceDocs(ctx, item)
	}
	r.AfterUpdate = func(ctx context.Context, before, after models.Driver) {
		if before.VehicleID != after.VehicleID {
			syncDriverVehiclePairing(ctx, repo, after)
		}
		_ = repo.SyncDriverComplianceDocs(ctx, after)
	}
	return r
}
