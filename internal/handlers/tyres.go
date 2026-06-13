package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// NewTyreResource is tyre CRUD with referential integrity (the vehicle must
// exist) and one current tyre per (vehicle, position) — a retired tyre is kept
// for history and doesn't block a fresh mount at the same position.
func NewTyreResource(repo *store.Repository) *Resource[models.Tyre, *models.Tyre] {
	r := &Resource[models.Tyre, *models.Tyre]{
		Repo: repo, Collection: repo.Tyres, Entity: "tyre", IDPrefix: "TYR",
	}
	check := func(c *gin.Context, t *models.Tyre) error {
		if err := validateVehicleExists(c.Request.Context(), repo, t.VehicleID); err != nil {
			return err
		}
		return validateTyrePosition(c.Request.Context(), repo, t)
	}
	r.BeforeCreate = check
	r.BeforeUpdate = check
	return r
}
