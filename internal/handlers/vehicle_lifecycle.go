// Vehicle asset lifecycle transitions — FR-VEH-06.
//
// A transition, not a record edit, for the same reason fuel-request approval is
// not a status write: the machine, the reason and the attribution are what make
// the record trustworthy, and a form that can set the field directly makes all
// three optional.
//
// The Next.js app has run this machine client-side for some time and PATCHed the
// result onto /api/vehicles/:id. That never worked — the columns did not exist
// until 0048 — but even once they do, a machine that lives only in the browser
// is advisory: anything reaching the gateway directly could ground a vehicle
// with no reason or reinstate a disposed one. So it runs here, and the record
// resource refuses to carry the fields (see ServerOwnedFields on the vehicle
// resource).
package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// VehicleLifecycle owns POST /api/vehicles/:id/lifecycle.
type VehicleLifecycle struct {
	Repo   *store.Repository
	Events *events.Bus
}

func (h *VehicleLifecycle) Register(rg *gin.RouterGroup) {
	rg.POST("/vehicles/:id/lifecycle",
		auth.RequirePerm("change_vehicle_lifecycle"), h.transition)
}

type vehicleLifecycleBody struct {
	To     string `json:"to"`
	Reason string `json:"reason"`
	// Disposal detail. Only read when To == "Disposed".
	DisposalMethod   string   `json:"disposalMethod"`
	DisposalDate     string   `json:"disposalDate"`
	DisposalProceeds *float64 `json:"disposalProceeds"`
	DisposalBuyer    string   `json:"disposalBuyer"`
}

func (h *VehicleLifecycle) transition(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vehicle id required"})
		return
	}

	var body vehicleLifecycleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Attribution comes from the verified session, never from the body —
	// otherwise the audit trail records whoever the caller says it was.
	actor := currentUser(c, h.Repo)
	now := nowISO()

	// The check runs inside Update, which holds the row FOR UPDATE. Two people
	// looking at the same vehicle can both press Ground; the second must be told
	// the transition no longer applies rather than silently reapplying it, and
	// checking outside the transaction would let both through.
	var refusal string
	var from string
	updated, err := h.Repo.Vehicles.Update(c.Request.Context(), id, func(v *models.Vehicle) {
		from = models.ToVehicleLifecycleState(v.LifecycleState)
		refusal = models.CheckVehicleLifecycleTransition(
			from, body.To, body.Reason, body.DisposalMethod)
		if refusal != "" {
			return
		}

		v.LifecycleState = strings.TrimSpace(body.To)
		v.LifecycleReason = strings.TrimSpace(body.Reason)
		v.LifecycleAt = now
		v.LifecycleBy = actor

		if v.LifecycleState == "Disposed" {
			v.DisposalMethod = strings.TrimSpace(body.DisposalMethod)
			v.DisposalDate = strings.TrimSpace(body.DisposalDate)
			if v.DisposalDate == "" {
				v.DisposalDate = now[:10]
			}
			v.DisposalBuyer = strings.TrimSpace(body.DisposalBuyer)
			// Nil and zero mean different things: a scrapped vehicle really did
			// realise nothing, and that is not the same as nobody recording it.
			v.DisposalProceeds = body.DisposalProceeds
		}
	})
	if err != nil {
		respondError(c, err)
		return
	}
	if refusal != "" {
		// 409 rather than 400: the request is well-formed, the fleet is just not
		// in a state where it applies.
		c.JSON(http.StatusConflict, gin.H{"error": refusal, "from": from})
		return
	}

	h.Repo.LogBest(c.Request.Context(), "lifecycle", "vehicle", id,
		from+" → "+updated.LifecycleState, actor)

	if h.Events != nil && h.Events.Enabled() {
		h.Events.PublishFleet(c.Request.Context(), events.TypeVehicleUpdated,
			events.FleetEventData(map[string]string{
				"vehicleId":      id,
				"plate":          updated.Plate,
				"lifecycleState": updated.LifecycleState,
				"previousState":  from,
			}), id, "")
	}

	c.JSON(http.StatusOK, updated)
}
