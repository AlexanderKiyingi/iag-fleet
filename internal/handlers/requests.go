package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// NewRequestResource is service-request CRUD with assignment validation, so the
// vehicle/driver dispatch guards enforced by the /assign workflow can't be
// bypassed by setting assignedVehicleId/assignedDriverId via a generic PATCH/PUT.
func NewRequestResource(repo *store.Repository) *Resource[models.ServiceRequest, *models.ServiceRequest] {
	r := &Resource[models.ServiceRequest, *models.ServiceRequest]{
		Repo:       repo,
		Collection: repo.Requests,
		Entity:     "service_request",
		IDPrefix:   "REQ",
	}
	r.BeforeCreate = func(c *gin.Context, item *models.ServiceRequest) error {
		return validateRequestAssignment(c.Request.Context(), repo, item)
	}
	r.BeforeUpdate = func(c *gin.Context, merged *models.ServiceRequest) error {
		// Only re-validate when the assignment actually changes, so routine
		// edits to an already-assigned request don't pay the lookup cost.
		if merged.AssignedVehicleID == "" && merged.AssignedDriverID == "" {
			return nil
		}
		if existing, err := repo.Requests.Get(c.Request.Context(), merged.ID); err == nil &&
			existing.AssignedVehicleID == merged.AssignedVehicleID &&
			existing.AssignedDriverID == merged.AssignedDriverID {
			return nil
		}
		return validateRequestAssignment(c.Request.Context(), repo, merged)
	}
	return r
}
