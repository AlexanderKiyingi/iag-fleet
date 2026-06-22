package handlers

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// NewRequestResource is service-request CRUD with assignment validation, so the
// vehicle/driver dispatch guards enforced by the /assign workflow can't be
// bypassed by setting assignedVehicleId/assignedDriverId via a generic PATCH/PUT.
//
// The frontend drives the whole request lifecycle through generic PATCH (not the
// dedicated /advance + /assign endpoints), so the linked-task cascade and the
// fleet.service_request.assigned event have to live on this path too — otherwise
// they only fire for the rarely-used workflow endpoints. requestTransitionEffects
// is the single place that logic lives; both paths call it.
func NewRequestResource(repo *store.Repository, ev *events.Bus) *Resource[models.ServiceRequest, *models.ServiceRequest] {
	r := &Resource[models.ServiceRequest, *models.ServiceRequest]{
		Repo:       repo,
		Collection: repo.Requests,
		Entity:     "service_request",
		IDPrefix:   "REQ",
		Events:     ev,
	}
	r.BeforeCreate = func(c *gin.Context, item *models.ServiceRequest) error {
		return validateRequestAssignment(c.Request.Context(), repo, item)
	}
	r.BeforeUpdate = func(c *gin.Context, merged *models.ServiceRequest) error {
		ctx := c.Request.Context()
		if existing, err := repo.Requests.Get(ctx, merged.ID); err == nil {
			// Stamp the approver the first time the request reaches "approved".
			if merged.Status == "approved" && existing.Status != "approved" {
				merged.ApprovedBy = currentUser(c, repo)
				merged.ApprovedAt = nowISO()
			}
			// Skip the dispatch re-validation when the assignment is unchanged,
			// so routine edits to an already-assigned request don't pay the
			// lookup cost.
			if existing.AssignedVehicleID == merged.AssignedVehicleID &&
				existing.AssignedDriverID == merged.AssignedDriverID {
				return nil
			}
		}
		// No assignment present (including clearing one) — nothing to validate.
		if merged.AssignedVehicleID == "" && merged.AssignedDriverID == "" {
			return nil
		}
		return validateRequestAssignment(ctx, repo, merged)
	}
	r.AfterUpdate = func(ctx context.Context, before, after models.ServiceRequest) {
		requestTransitionEffects(ctx, repo, ev, before, after)
	}
	return r
}

// requestTransitionEffects applies the cross-entity side effects of a
// service-request status change: it cascades the linked task's state and emits
// the fleet.service_request.assigned event. Centralising it here keeps the
// generic PATCH path (Resource.AfterUpdate) and the dedicated /advance + /assign
// workflow endpoints in lockstep — previously the cascade and event lived only
// in the workflow handlers, so the PATCH-based frontend silently dropped both.
func requestTransitionEffects(ctx context.Context, repo *store.Repository, ev *events.Bus, before, after models.ServiceRequest) {
	// Task-state cascade — only when the status actually changed.
	if after.TaskID != "" && before.Status != after.Status {
		if state, completedAt := taskStateForRequestStatus(after.Status); state != "" {
			// Swallow the inner error: a missing task or permission shouldn't
			// unwind the request transition the caller just asked for.
			_, _ = repo.Tasks.Update(ctx, after.TaskID, func(t *models.TaskItem) {
				t.State = state
				if completedAt != "" {
					t.CompletedAt = completedAt
				}
			})
		}
	}

	// Assignment event — fire once, the first time the request reaches
	// "assigned" with both a vehicle and driver set.
	newlyAssigned := after.Status == "assigned" && before.Status != "assigned"
	if newlyAssigned && after.AssignedVehicleID != "" && after.AssignedDriverID != "" &&
		ev != nil && ev.Enabled() {
		ev.PublishFleet(ctx, events.TypeServiceRequestAssigned, events.FleetEventData(map[string]string{
			"requestId": after.ID,
			"vehicleId": after.AssignedVehicleID,
			"driverId":  after.AssignedDriverID,
		}), after.ID, after.ID)
	}
}

// taskStateForRequestStatus maps a service-request status to the linked task's
// state. Mirrors what requests/[id]/page.tsx used to do client-side:
//
//	reviewed            → in-review
//	approved | assigned → in-progress
//	completed           → done (also stamps completedAt)
//
// Other statuses (submitted, received, rejected) don't move the task.
func taskStateForRequestStatus(status string) (state, completedAt string) {
	switch status {
	case "reviewed":
		return "in-review", ""
	case "approved", "assigned":
		return "in-progress", ""
	case "completed":
		return "done", nowISO()
	default:
		return "", ""
	}
}
