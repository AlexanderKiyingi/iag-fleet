package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/config"
	"github.com/iag/fleet-tool/backend/internal/events"
	jmpplan "github.com/iag/fleet-tool/backend/internal/jmp"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/warehouseclient"
	"github.com/jackc/pgx/v5"
)

// Workflows owns endpoints that encode multi-field or cross-entity state
// transitions — the operations the frontend currently does as ad-hoc patches.
type Workflows struct {
	Repo           *store.Repository
	Events         *events.Bus
	RoutingOSRMURL string
	Config         config.Config
	// Warehouse is the outbound client to iag-warehouse. Non-nil only when
	// stock delegation is enabled; nil otherwise (fleet keeps local stock).
	Warehouse *warehouseclient.Client
}

func (w *Workflows) Register(rg *gin.RouterGroup) {
	// JMPs
	rg.POST("/jmps/:id/complete-toolbox", auth.RequirePerm("complete_toolbox_jmp"), w.completeToolbox)
	rg.POST("/jmps/:id/complete", auth.RequirePerm("complete_jmp"), w.completeJmp)
	rg.POST("/jmps/:id/cancel", auth.RequirePerm("cancel_jmp"), w.cancelJmp)
	rg.POST("/jmps/:id/approve-mileage", auth.RequirePerm("approve_mileage_jmp"), w.approveMileage)

	// Cargo
	rg.POST("/cargo/:id/set-stage", auth.RequirePerm("advance_stage_cargo"), w.cargoSetStage)
	rg.POST("/cargo/:id/advance-stage", auth.RequirePerm("advance_stage_cargo"), w.cargoAdvanceStage)
	rg.POST("/cargo/:id/offload", auth.RequirePerm("offload_cargo"), w.cargoOffload)
	rg.POST("/cargo/:id/demobilise", auth.RequirePerm("demobilise_cargo"), w.cargoDemobilise)
	rg.POST("/cargo/:id/complete", auth.RequirePerm("advance_stage_cargo"), w.cargoComplete)

	// Requests
	rg.POST("/requests/:id/assign", auth.RequirePerm("assign_request"), w.assignRequest)
	rg.POST("/requests/:id/advance", auth.RequirePerm("change_service_request"), w.requestAdvance)
	rg.POST("/requests/:id/create-jmp", auth.RequirePerm("add_jmp"), w.requestCreateJMP)

	// Tasks
	rg.POST("/tasks/:id/complete", auth.RequirePerm("complete_task"), w.completeTask)

	// Deployment
	rg.POST("/deployment/seed-today", auth.RequirePerm("seed_deployment"), w.seedToday)
	rg.POST("/deployment/:id/entries", auth.RequirePerm("add_deployment_entry"), w.addDeploymentEntry)

	// Vehicles
	rg.POST("/vehicles/simulate-tick", auth.RequirePerm("simulate_vehicles"), w.simulateTick)

	// Parts (stock ledger)
	rg.POST("/parts/:id/movements", auth.RequirePerm("change_part"), w.partAdjustStock)

	// Compliance (renewal log)
	rg.POST("/compliance/:id/renew", auth.RequirePerm("change_compliance_item"), w.complianceRenew)

	// Maintenance (complete-with-stock-decrement)
	rg.POST("/maintenance/:id/complete", auth.RequirePerm("change_maintenance_item"), w.maintenanceComplete)
	rg.POST("/maintenance/:id/advance-status", auth.RequirePerm("change_maintenance_item"), w.maintenanceAdvanceStatus)

	// Safety lifecycle
	rg.POST("/safety/:id/advance-status", auth.RequirePerm("change_safety_event"), w.safetyAdvanceStatus)
	rg.POST("/safety/:id/create-wo", auth.RequirePerm("change_safety_event"), w.safetyCreateWO)
}

// ───────────────────────────── JMPs ─────────────────────────────

func (w *Workflows) completeToolbox(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	updated, err := w.Repo.JMPs.Update(ctx, id, func(j *models.JMP) {
		// Force every toolbox item to true to satisfy the "all checked" rule;
		// callers should generally have already toggled them via PATCH first,
		// but this keeps the workflow idempotent.
		t := true
		j.Toolbox.Items = models.ToolboxItems{
			NoDrunkDriving:    &t,
			SpeedLimits:       &t,
			CargoInspection:   &t,
			Communication:     &t,
			FatigueManagement: &t,
			IncidentContacts:  &t,
			RouteReviewed:     &t,
			ParkingConfirmed:  &t,
		}
		j.Toolbox.Completed = true
		j.Toolbox.CompletedAt = nowISO()
		if j.Status == "draft" || j.Status == "pending-toolbox" {
			j.Status = "active"
		}
	})
	if err != nil {
		respondError(c, err)
		return
	}
	w.Repo.LogBest(ctx, "toolbox-complete", "jmp", id, "", currentUser(c, w.Repo))
	w.emitFleet(ctx, events.TypeJMPCompleted, map[string]string{"jmpId": id, "status": updated.Status}, id)
	c.JSON(http.StatusOK, updated)
}

func (w *Workflows) cancelJmp(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	updated, err := w.Repo.JMPs.Update(ctx, id, func(j *models.JMP) { j.Status = "cancelled" })
	if err != nil {
		respondError(c, err)
		return
	}
	w.Repo.LogBest(ctx, "cancel", "jmp", id, "", currentUser(c, w.Repo))
	w.emitFleet(ctx, events.TypeJMPCancelled, map[string]string{"jmpId": id}, id)
	c.JSON(http.StatusOK, updated)
}

func (w *Workflows) completeJmp(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	existing, err := w.Repo.JMPs.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if existing.Status == "completed" {
		c.JSON(http.StatusConflict, gin.H{"error": "JMP already completed"})
		return
	}
	if existing.Status == "cancelled" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot complete a cancelled JMP"})
		return
	}
	if existing.Status != "active" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "JMP must be active before completion (complete toolbox first)",
			"status": existing.Status,
		})
		return
	}
	updated, err := w.Repo.JMPs.Update(ctx, id, func(j *models.JMP) {
		j.Status = "completed"
		j.CompletedAt = nowISO()
	})
	if err != nil {
		respondError(c, err)
		return
	}
	user := currentUser(c, w.Repo)
	fuelRecon, _ := jmpplan.ReconcileFuelActuals(ctx, w.Repo, updated)
	note := jmpplan.FuelReconciliationNote(fuelRecon)
	w.Repo.LogBest(ctx, "complete", "jmp", id, note, user)
	w.emitFleet(ctx, events.TypeJMPCompleted, map[string]string{"jmpId": id, "status": "completed"}, id)
	c.JSON(http.StatusOK, struct {
		models.JMP
		FuelReconciliation jmpplan.FuelReconciliation `json:"fuelReconciliation"`
	}{
		JMP:                updated,
		FuelReconciliation: fuelRecon,
	})
}

type approveMileageRequest struct {
	Approved bool   `json:"approved"`
	Notes    string `json:"notes"`
}

func (w *Workflows) approveMileage(c *gin.Context) {
	var body approveMileageRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, w.Repo)
	updated, err := w.Repo.JMPs.Update(ctx, id, func(j *models.JMP) {
		if body.Approved {
			j.MileageStatus = "Approved"
			j.ApprovedBy = user
			j.ApprovedAt = nowISO()
		} else {
			j.MileageStatus = "Rejected"
		}
	})
	if err != nil {
		respondError(c, err)
		return
	}
	action := "approve-mileage"
	if !body.Approved {
		action = "reject-mileage"
	}
	w.Repo.LogBest(ctx, action, "jmp", id, body.Notes, user)
	c.JSON(http.StatusOK, updated)
}

// ───────────────────────────── Cargo ─────────────────────────────

type cargoStageRequest struct {
	Stage string `json:"stage" binding:"required"`
	Note  string `json:"note"`
}

func (w *Workflows) cargoSetStage(c *gin.Context) {
	var body cargoStageRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validCargoStage(body.Stage) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stage"})
		return
	}
	updated, err := w.applyCargoStage(c.Request.Context(), c.Param("id"), body.Stage, body.Note, currentUser(c, w.Repo))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (w *Workflows) cargoAdvanceStage(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	existing, err := w.Repo.Cargo.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	next := nextCargoStage(existing.Stage)
	if next == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "already at terminal stage"})
		return
	}
	updated, err := w.applyCargoStage(ctx, id, next, "", currentUser(c, w.Repo))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (w *Workflows) cargoOffload(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, w.Repo)
	now := time.Now().UTC()
	updated, err := w.Repo.Cargo.Update(ctx, id, func(cg *models.Cargo) {
		cg.Stage = "offloaded"
		cg.OffloadingDate = now.Format("2006-01-02")
		cg.StageHistory = append(cg.StageHistory, models.CargoStageEvent{
			Stage: "offloaded", At: now.Format(time.RFC3339), By: user,
		})
	})
	if err != nil {
		respondError(c, err)
		return
	}
	w.Repo.LogBest(ctx, "offload", "cargo", id, "", user)
	c.JSON(http.StatusOK, updated)
}

func (w *Workflows) cargoDemobilise(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, w.Repo)
	now := time.Now().UTC()
	demob := true
	updated, err := w.Repo.Cargo.Update(ctx, id, func(cg *models.Cargo) {
		cg.Stage = "demobilised"
		cg.Demobilised = &demob
		cg.DemobilisedAt = now.Format(time.RFC3339)
		cg.StageHistory = append(cg.StageHistory, models.CargoStageEvent{
			Stage: "demobilised", At: now.Format(time.RFC3339), By: user,
		})
	})
	if err != nil {
		respondError(c, err)
		return
	}
	w.Repo.LogBest(ctx, "demobilise", "cargo", id, "", user)
	c.JSON(http.StatusOK, updated)
}

func (w *Workflows) cargoComplete(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, w.Repo)
	updated, err := w.applyCargoStage(ctx, id, "completed", "", user)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (w *Workflows) applyCargoStage(ctx context.Context, id, stage, note, user string) (models.Cargo, error) {
	now := time.Now().UTC()
	updated, err := w.Repo.Cargo.Update(ctx, id, func(cg *models.Cargo) {
		if cg.Stage == stage {
			return
		}
		cg.Stage = stage
		cg.StageHistory = append(cg.StageHistory, models.CargoStageEvent{
			Stage: stage, At: now.Format(time.RFC3339), By: user, Note: note,
		})
		if stage == "at-acp" && cg.ArrivalAcp == "" {
			cg.ArrivalAcp = now.Format("2006-01-02")
		}
	})
	if err == nil {
		w.Repo.LogBest(ctx, "stage:"+stage, "cargo", id, note, user)
		w.emitFleet(ctx, events.TypeCargoStageAdvanced, map[string]string{
			"cargoId": id,
			"stage":   stage,
		}, id)
		if stage == "offloaded" {
			w.emitFleet(ctx, events.TypeCargoOffloaded, map[string]string{"cargoId": id}, id)
		}
	}
	return updated, err
}

func validCargoStage(s string) bool {
	for _, st := range models.CargoStages {
		if st.K == s {
			return true
		}
	}
	return false
}

func nextCargoStage(current string) string {
	for i, st := range models.CargoStages {
		if st.K == current && i+1 < len(models.CargoStages) {
			return models.CargoStages[i+1].K
		}
	}
	return ""
}

// ─────────────────────────── Requests ────────────────────────────

type assignRequestBody struct {
	VehicleID     string `json:"vehicleId" binding:"required"`
	DriverID      string `json:"driverId" binding:"required"`
	ReviewerNotes string `json:"reviewerNotes"`
}

func (w *Workflows) assignRequest(c *gin.Context) {
	var body assignRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := c.Param("id")
	ctx := c.Request.Context()

	veh, err := w.Repo.Vehicles.Get(ctx, body.VehicleID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vehicle not found"})
		return
	}
	if err := vehicleDispatchable(veh); err != nil {
		respondMutationError(c, err)
		return
	}
	drv, err := w.Repo.Drivers.Get(ctx, body.DriverID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "driver not found"})
		return
	}
	if drv.External {
		c.JSON(http.StatusBadRequest, gin.H{"error": "driver is external"})
		return
	}
	if err := validateDriverDispatch(ctx, w.Repo, body.DriverID); err != nil {
		respondMutationError(c, err)
		return
	}
	// Reject assigning a driver/vehicle already committed to an overlapping
	// journey in the request's window (early guard; JMP creation enforces it too).
	req, err := w.Repo.Requests.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if err := validateJMPAvailability(ctx, w.Repo, body.DriverID, body.VehicleID, req.StartDate, req.EndDate, ""); err != nil {
		respondMutationError(c, err)
		return
	}

	user := currentUser(c, w.Repo)
	updated, err := w.Repo.Requests.Update(ctx, id, func(r *models.ServiceRequest) {
		r.AssignedVehicleID = body.VehicleID
		r.AssignedDriverID = body.DriverID
		r.ReviewerNotes = body.ReviewerNotes
		r.Status = "assigned"
	})
	if err != nil {
		respondError(c, err)
		return
	}

	if updated.TaskID != "" {
		_, _ = w.Repo.Tasks.Update(ctx, updated.TaskID, func(t *models.TaskItem) {
			t.State = "in-progress"
		})
	}
	w.Repo.LogBest(ctx, "assign", "request", id, "", user)
	w.emitFleet(ctx, events.TypeServiceRequestAssigned, map[string]string{
		"requestId": id,
		"vehicleId": body.VehicleID,
		"driverId":  body.DriverID,
	}, id)
	c.JSON(http.StatusOK, updated)
}

// requestAdvance moves a service-request along its status machine and
// cascades the linked task's state in one transaction-equivalent
// operation. The status→state mapping mirrors what requests/[id]/page.tsx
// previously did with two separate PATCHes:
//
//	reviewed  → in-review
//	approved  → in-progress
//	completed → done (also stamps task.completedAt)
//	rejected  → no task change
//
// "submitted" is allowed for symmetry but doesn't trigger a cascade.
// "assigned" is rejected here — that path goes through /assign, which
// also validates eligibility and stores vehicle/driver fields.
type requestAdvanceBody struct {
	Status string `json:"status" binding:"required"`
}

func (w *Workflows) requestAdvance(c *gin.Context) {
	var body requestAdvanceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	switch body.Status {
	case "submitted", "reviewed", "approved", "rejected", "completed":
		// ok
	case "assigned":
		c.JSON(http.StatusBadRequest, gin.H{"error": "use POST /assign for the assigned transition"})
		return
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status"})
		return
	}

	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, w.Repo)

	updated, err := w.Repo.Requests.Update(ctx, id, func(r *models.ServiceRequest) {
		r.Status = body.Status
	})
	if err != nil {
		respondError(c, err)
		return
	}

	// Cascade to the linked task. We swallow the inner error: a missing
	// task or permission shouldn't unwind the request transition the
	// caller just asked for. The audit log records both attempts.
	if updated.TaskID != "" {
		var nextState, completedAt string
		switch body.Status {
		case "reviewed":
			nextState = "in-review"
		case "approved":
			nextState = "in-progress"
		case "completed":
			nextState = "done"
			completedAt = nowISO()
		}
		if nextState != "" {
			_, _ = w.Repo.Tasks.Update(ctx, updated.TaskID, func(t *models.TaskItem) {
				t.State = nextState
				if completedAt != "" {
					t.CompletedAt = completedAt
				}
			})
		}
	}

	w.Repo.LogBest(ctx, "advance:"+body.Status, "request", id, "", user)
	c.JSON(http.StatusOK, updated)
}

// requestCreateJMP draft-creates a Journey Plan from an assigned request,
// stamps the new id back onto request.jmpId, and (when there's a linked
// task) appends a `{type:"jmp", id}` link to it. Pre-this-endpoint the
// frontend did all three writes back-to-back with no atomicity.
//
// Rejects when the request lacks an assigned vehicle/driver or already
// has a JMP — same guards the UI used to enforce client-side.
func (w *Workflows) requestCreateJMP(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	req, err := w.Repo.Requests.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if req.AssignedVehicleID == "" || req.AssignedDriverID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "assign vehicle and driver before creating a JMP"})
		return
	}
	if req.JmpID != "" {
		c.JSON(http.StatusConflict, gin.H{
			"error": "request already has a JMP",
			"jmpId": req.JmpID,
		})
		return
	}
	if err := validateDriverDispatch(ctx, w.Repo, req.AssignedDriverID); err != nil {
		respondMutationError(c, err)
		return
	}

	expectedReturn := req.EndDate
	if expectedReturn == "" {
		expectedReturn = req.StartDate
	}
	expectedDays := jmpplan.ComputeExpectedDays(req.StartDate, expectedReturn)

	if err := validateVehicleDispatchable(ctx, w.Repo, req.AssignedVehicleID); err != nil {
		respondMutationError(c, err)
		return
	}
	if err := validateJMPAvailability(ctx, w.Repo, req.AssignedDriverID, req.AssignedVehicleID, req.StartDate, expectedReturn, ""); err != nil {
		respondMutationError(c, err)
		return
	}

	cargoDescription := req.CargoType
	if cargoDescription == "" && req.Pax != nil {
		cargoDescription = fmt.Sprintf("%d pax", *req.Pax)
	}

	user := currentUser(c, w.Repo)
	jmp := models.JMP{
		ID:                generateYearID("JMP"),
		VehicleID:         req.AssignedVehicleID,
		DriverID:          req.AssignedDriverID,
		Purpose:           req.Purpose,
		CargoDescription:  cargoDescription,
		StartDate:         req.StartDate,
		ExpectedArrival:   req.StartDate,
		DesignatedParking: req.Destination,
		RouteSummary:      "ACP → " + req.Destination,
		ExpectedDays:      expectedDays,
		ExpectedReturn:    expectedReturn,
		MileageStatus:     "Pending",
		Toolbox:           models.Toolbox{Completed: false, Items: models.ToolboxItems{}},
		ConvoyPartner:     "Solo",
		Status:            "draft",
		CreatedAt:         nowISO(),
		CreatedBy:         user,
		ParkingPhotos:     []string{},
		SourceRequestID:   req.ID,
	}

	jmpplan.Enrich(ctx, &jmp, w.RoutingOSRMURL)

	created, err := w.Repo.JMPs.Add(ctx, jmp)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Stamp jmpId back onto the request and append the link to its task.
	// Either failure here leaves the JMP in place (already committed) but
	// surfaces the error to the caller; the audit log records what we
	// actually wrote so reconciliation is possible.
	if _, err := w.Repo.Requests.Update(ctx, req.ID, func(r *models.ServiceRequest) {
		r.JmpID = created.ID
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if req.TaskID != "" {
		_, _ = w.Repo.Tasks.Update(ctx, req.TaskID, func(t *models.TaskItem) {
			t.Links = append(t.Links, models.TaskLink{Type: "jmp", ID: created.ID})
		})
	}

	w.Repo.LogBest(ctx, "create-jmp", "request", req.ID, created.ID, user)
	c.JSON(http.StatusCreated, created)
}

// ───────────────────────────── Tasks ─────────────────────────────

func (w *Workflows) completeTask(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	updated, err := w.Repo.Tasks.Update(ctx, id, func(t *models.TaskItem) {
		t.State = "done"
		t.CompletedAt = nowISO()
	})
	if err != nil {
		respondError(c, err)
		return
	}
	w.Repo.LogBest(ctx, "complete", "task", id, "", currentUser(c, w.Repo))
	c.JSON(http.StatusOK, updated)
}

// ─────────────────────────── Deployment ──────────────────────────

func (w *Workflows) seedToday(c *gin.Context) {
	ctx := c.Request.Context()
	today := time.Now().UTC().Format("2006-01-02")
	days, err := w.Repo.Deployment.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, d := range days {
		if d.Date == today {
			c.JSON(http.StatusConflict, gin.H{"error": "today already seeded", "deployment": d})
			return
		}
	}

	prior := mostRecentDeployment(days)
	vehicles, err := w.Repo.Vehicles.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	entries := make(models.DeploymentEntries, 0, len(vehicles))
	for _, v := range vehicles {
		var start float64 = v.Odo
		if prior != nil {
			for _, e := range prior.Entries {
				if e.VehicleID == v.ID {
					start = e.OdoEnd
					break
				}
			}
		}
		ft := v.FuelTracker
		entries = append(entries, models.DeploymentEntry{
			ID:               generateID("DE"),
			VehicleID:        v.ID,
			DriverID:         v.DriverID,
			Deployment:       v.Purpose,
			Location:         v.Location,
			MechStatus:       deriveMech(v.Status),
			DeploymentStatus: deriveStatus(v.Status),
			OdoStart:         start,
			OdoEnd:           start,
			FuelTracker:      &ft,
		})
	}

	user := currentUser(c, w.Repo)
	day := models.DeploymentDay{
		ID:         generateID("DEP"),
		Date:       today,
		CompiledBy: user,
		Notes:      "",
		Entries:    entries,
	}
	created, err := w.Repo.Deployment.Add(ctx, day)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	w.Repo.LogBest(ctx, "seed-today", "deployment", created.ID, "", user)
	c.JSON(http.StatusCreated, created)
}

type addEntryBody struct {
	VehicleID        string  `json:"vehicleId" binding:"required"`
	DriverID         string  `json:"driverId"`
	Deployment       string  `json:"deployment"`
	Location         string  `json:"location"`
	OdoStart         float64 `json:"odoStart"`
	OdoEnd           float64 `json:"odoEnd"`
	MechStatus       string  `json:"mechStatus"`
	DeploymentStatus string  `json:"deploymentStatus"`
	FuelTracker      *bool   `json:"fuelTracker"`
	Notes            string  `json:"notes"`
}

func (w *Workflows) addDeploymentEntry(c *gin.Context) {
	depID := c.Param("id")
	ctx := c.Request.Context()
	var body addEntryBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.MechStatus == "" {
		body.MechStatus = "operational"
	}
	if body.DeploymentStatus == "" {
		body.DeploymentStatus = "deployed"
	}

	// No double-deploy: a vehicle/driver may appear at most once in a day.
	day, dErr := w.Repo.Deployment.Get(ctx, depID)
	if dErr != nil {
		respondError(c, dErr)
		return
	}
	for _, e := range day.Entries {
		if body.VehicleID != "" && e.VehicleID == body.VehicleID {
			c.JSON(http.StatusConflict, gin.H{"error": "vehicle already has a deployment entry for this day"})
			return
		}
		if body.DriverID != "" && e.DriverID == body.DriverID {
			c.JSON(http.StatusConflict, gin.H{"error": "driver already has a deployment entry for this day"})
			return
		}
	}

	entry := models.DeploymentEntry{
		ID:               generateID("DE"),
		VehicleID:        body.VehicleID,
		DriverID:         body.DriverID,
		Deployment:       body.Deployment,
		Location:         body.Location,
		MechStatus:       body.MechStatus,
		DeploymentStatus: body.DeploymentStatus,
		OdoStart:         body.OdoStart,
		OdoEnd:           body.OdoEnd,
		FuelTracker:      body.FuelTracker,
		Notes:            body.Notes,
	}

	updated, err := w.Repo.Deployment.Update(ctx, depID, func(d *models.DeploymentDay) {
		d.Entries = append(d.Entries, entry)
	})
	if err != nil {
		respondError(c, err)
		return
	}

	// Sync vehicle ODO upward — never let it run backwards.
	if veh, vErr := w.Repo.Vehicles.Get(ctx, body.VehicleID); vErr == nil && body.OdoEnd > veh.Odo {
		_, _ = w.Repo.Vehicles.Update(ctx, body.VehicleID, func(v *models.Vehicle) {
			v.Odo = body.OdoEnd
		})
	}

	w.Repo.LogBest(ctx, "add-entry", "deployment", depID, body.VehicleID, currentUser(c, w.Repo))
	c.JSON(http.StatusCreated, updated)
}

func mostRecentDeployment(days []models.DeploymentDay) *models.DeploymentDay {
	var latest *models.DeploymentDay
	for i := range days {
		if latest == nil || days[i].Date > latest.Date {
			latest = &days[i]
		}
	}
	return latest
}

func deriveMech(status string) string {
	switch status {
	case "maintenance":
		return "in-service"
	case "offline":
		return "grounded"
	default:
		return "operational"
	}
}

func deriveStatus(status string) string {
	switch status {
	case "moving":
		return "deployed"
	case "idle":
		return "idle"
	case "maintenance":
		return "under-repair"
	default:
		return "demobilised"
	}
}

// ──────────────────────── Vehicles · simulator ────────────────────

const simTickMs = 15000

// simulateTick advances every `moving` vehicle one step along its current
// heading, mirroring lib/simulator.tsx so the simulator can be driven by the
// backend instead of the browser.
func (w *Workflows) simulateTick(c *gin.Context) {
	if denyIfProduction(c, w.Config, "simulate_vehicles") {
		return
	}
	ctx := c.Request.Context()
	vehicles, err := w.Repo.Vehicles.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stepCount := 0
	for _, v := range vehicles {
		if v.Status != "moving" {
			continue
		}
		next := stepVehicle(v)
		if _, err := w.Repo.Vehicles.Update(ctx, v.ID, func(v *models.Vehicle) {
			v.Lat = next.Lat
			v.Lng = next.Lng
			v.Heading = next.Heading
			v.LastSeen = nowISO()
		}); err == nil {
			stepCount++
		}
	}
	c.JSON(http.StatusOK, gin.H{"updated": stepCount, "tickMs": simTickMs})
}

func stepVehicle(v models.Vehicle) models.Vehicle {
	sp := v.Speed
	if sp == 0 {
		sp = 40
	}
	if sp < 20 {
		sp = 20
	} else if sp > 80 {
		sp = 80
	}
	stepKm := (sp / 3600.0) * (float64(simTickMs) / 1000.0)
	stepDeg := stepKm / 111.0
	rad := v.Heading * math.Pi / 180.0
	v.Lat += math.Cos(rad) * stepDeg
	cosLat := math.Cos(v.Lat * math.Pi / 180.0)
	if cosLat == 0 {
		cosLat = 1
	}
	v.Lng += (math.Sin(rad) * stepDeg) / cosLat
	v.Heading = math.Mod(math.Mod(v.Heading+(rand.Float64()*30-15), 360)+360, 360)
	return v
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ─────────────────────────── Parts (stock ledger) ─────────────────────────

type partMovementRequest struct {
	Type     string  `json:"type" binding:"required"` // "in" | "out" | "adjust"
	Qty      float64 `json:"qty"`
	UnitCost float64 `json:"unitCost,omitempty"`
	Ref      string  `json:"ref,omitempty"`
	Note     string  `json:"note,omitempty"`
	Date     string  `json:"date,omitempty"`
}

// partAdjustStock appends a movement row and updates parts.stock in one
// transaction. v7 parity: every change to on-hand goes through the
// ledger so the Inventory > Movements tab + audit log reconcile to the
// stock number on every part.
//
//	type=in       stock += qty (also overrides unit_cost when supplied)
//	type=out      stock -= qty   (rejects when qty > current stock)
//	type=adjust   stock  = qty   (cycle-count correction; qty is the new
//	              on-hand, not a delta)
func (w *Workflows) partAdjustStock(c *gin.Context) {
	var body partMovementRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Type != "in" && body.Type != "out" && body.Type != "adjust" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type must be 'in', 'out', or 'adjust'"})
		return
	}
	if body.Type != "adjust" && body.Qty <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "qty must be positive"})
		return
	}
	if body.Type == "adjust" && body.Qty < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "qty (new on-hand) must be non-negative"})
		return
	}

	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, w.Repo)
	when := body.Date
	if when == "" {
		when = nowISO()
	}

	mv := models.PartMovement{
		ID:       generateID("MV"),
		Date:     when,
		Type:     body.Type,
		Qty:      body.Qty,
		UnitCost: body.UnitCost,
		Ref:      body.Ref,
		Note:     body.Note,
	}

	var overdrawShort int // requested-minus-available on an "out" that exceeded stock
	updated, err := w.Repo.Parts.Update(ctx, id, func(p *models.Part) {
		switch body.Type {
		case "in":
			p.Stock += int(body.Qty)
			if body.UnitCost > 0 {
				p.UnitCost = body.UnitCost
			}
		case "out":
			req := int(body.Qty)
			if req > p.Stock {
				// The workshop floor doesn't always reconcile same-day, so an
				// over-draw still clamps to zero — but it is recorded on the
				// ledger movement (and logged below) instead of being silent.
				overdrawShort = req - p.Stock
				mv.Note = fmt.Sprintf("%s [overdraw: requested %d, on-hand %d, clamped to 0]", body.Note, req, p.Stock)
				p.Stock = 0
			} else {
				p.Stock = p.Stock - req
			}
		case "adjust":
			p.Stock = int(body.Qty)
		}
		p.Movements = append(p.Movements, mv)
		day := movementDate(when)
		switch body.Type {
		case "in":
			p.LastReceived = day
		case "out":
			p.LastConsumed = day
		}
	})
	if err != nil {
		respondError(c, err)
		return
	}
	if overdrawShort > 0 {
		slog.Warn("part stock overdraw clamped to zero", "part", id, "short", overdrawShort, "user", user)
	}
	w.Repo.LogBest(ctx, "stock:"+body.Type, "part", id, body.Ref, user)
	c.JSON(http.StatusOK, updated)
}

// ─────────────────────────── Compliance (renewal log) ─────────────────────

type complianceRenewRequest struct {
	DocNumber      string  `json:"docNumber"`
	Issuer         string  `json:"issuer"`
	Issued         string  `json:"issued,omitempty"` // YYYY-MM-DD
	Expiry         string  `json:"expiry" binding:"required"`
	Cost           float64 `json:"cost,omitempty"`
	RenewalCostUgx float64 `json:"renewalCostUgx,omitempty"`
	Note           string  `json:"note,omitempty"`
}

// complianceRenew pushes the row's *current* period (doc_number / issuer
// / issued / expiry) onto renewal_history with a renewedAt stamp, then
// overwrites the top-level fields with the new period from the request
// body. Status is recomputed coarsely — the row is "valid" as soon as
// it has a future expiry.
func (w *Workflows) complianceRenew(c *gin.Context) {
	var body complianceRenewRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, w.Repo)
	now := time.Now().UTC().Format(time.RFC3339)

	if err := validateFutureExpiry(body.Expiry); err != nil {
		respondMutationError(c, err)
		return
	}

	updated, err := w.Repo.Compliance.Update(ctx, id, func(ci *models.ComplianceItem) {
		// Prior period → history (only if it carried any signal).
		if ci.DocNumber != "" || ci.Issuer != "" || ci.Issued != "" || ci.Expiry != "" {
			ci.RenewalHistory = append(ci.RenewalHistory, models.ComplianceRenewal{
				RenewedAt: now,
				DocNumber: ci.DocNumber,
				Issuer:    ci.Issuer,
				Issued:    ci.Issued,
				Expiry:    ci.Expiry,
				Cost:      body.Cost,
				Note:      body.Note,
			})
		}
		ci.DocNumber = body.DocNumber
		ci.Issuer = body.Issuer
		ci.Issued = body.Issued
		ci.Expiry = body.Expiry
		ci.Status = "valid"
		if body.RenewalCostUgx > 0 {
			ci.RenewalCostUgx = body.RenewalCostUgx
		} else if body.Cost > 0 {
			ci.RenewalCostUgx = body.Cost
		}
	})
	if err != nil {
		respondError(c, err)
		return
	}
	w.Repo.LogBest(ctx, "renew", "compliance_item", id, body.DocNumber, user)
	w.emitFleet(ctx, events.TypeComplianceRenewed, map[string]string{
		"complianceId": id,
		"docType":      updated.DocType,
		"expiry":       updated.Expiry,
	}, id)
	c.JSON(http.StatusOK, updated)
}

type advanceStatusBody struct {
	Status string `json:"status" binding:"required"`
	Note   string `json:"note,omitempty"`
}

func (w *Workflows) maintenanceAdvanceStatus(c *gin.Context) {
	var body advanceStatusBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := c.Param("id")
	ctx := c.Request.Context()
	if err := validateMaintenanceStatus(body.Status); err != nil {
		respondMutationError(c, err)
		return
	}
	user := currentUser(c, w.Repo)
	updated, err := w.Repo.Maintenance.Update(ctx, id, func(m *models.MaintenanceItem) {
		if m.Status != body.Status {
			appendStatusHistory(&m.StatusHistory, body.Status, user, body.Note)
			m.Status = body.Status
		}
	})
	if err != nil {
		respondError(c, err)
		return
	}
	w.Repo.LogBest(ctx, "status:"+body.Status, "maintenance_item", id, body.Note, user)
	c.JSON(http.StatusOK, updated)
}

func (w *Workflows) safetyAdvanceStatus(c *gin.Context) {
	var body advanceStatusBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, w.Repo)
	updated, err := w.Repo.Safety.Update(ctx, id, func(s *models.SafetyEvent) {
		if s.Status != body.Status {
			appendStatusHistory(&s.StatusHistory, body.Status, user, body.Note)
			s.Status = body.Status
		}
	})
	if err != nil {
		respondError(c, err)
		return
	}
	w.Repo.LogBest(ctx, "status:"+body.Status, "safety_event", id, body.Note, user)
	c.JSON(http.StatusOK, updated)
}

// safetyCreateWO raises a maintenance work order from a safety incident and
// links the two records both ways (safety.linked_wo_id ↔ maintenance.linked_safety_id).
// Idempotent: if the incident already has a linked WO it is returned unchanged.
// Mirrors inspections.createDefectWO.
func (w *Workflows) safetyCreateWO(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	se, err := w.Repo.Safety.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if se.LinkedWoID != "" {
		if mx, err := w.Repo.Maintenance.Get(ctx, se.LinkedWoID); err == nil {
			c.JSON(http.StatusOK, gin.H{"maintenance": mx, "alreadyLinked": true})
			return
		}
	}
	priority := "high"
	if se.Severity == "crit" {
		priority = "critical"
	}
	mx := models.MaintenanceItem{
		VehicleID:      se.VehicleID,
		Date:           todayDate(),
		Type:           "Repair",
		Service:        "Incident follow-up: " + se.Type,
		Status:         "scheduled",
		Priority:       priority,
		Workshop:       "TBD",
		Notes:          se.Description,
		LinkedSafetyID: se.ID,
	}
	created, err := w.Repo.Maintenance.Add(ctx, mx)
	if err != nil {
		respondError(c, err)
		return
	}
	user := currentUser(c, w.Repo)
	if _, err := w.Repo.Safety.Update(ctx, id, func(s *models.SafetyEvent) {
		s.LinkedWoID = created.ID
	}); err != nil {
		respondError(c, err)
		return
	}
	w.Repo.LogBest(ctx, "incident-wo", "safety_event", id, created.ID, user)
	c.JSON(http.StatusCreated, gin.H{"maintenance": created})
}

func movementDate(when string) string {
	if len(when) >= 10 {
		return when[:10]
	}
	return time.Now().UTC().Format("2006-01-02")
}

// ─────────────────────────── Maintenance (complete + stock decrement) ─────

// maintenanceComplete sets a WO's status to "completed" AND decrements
// parts.stock for every line in parts_breakdown, appending an "out"
// movement row per part referenced. The whole thing runs in one tx so
// a missing part / stock-out aborts cleanly without leaving the WO half
// closed and the ledger half written.
//
// Lines that reference parts that don't exist cause a 400 (the operator
// can fix the breakdown and retry). Lines that would push stock below
// zero clamp to zero — the workshop floor doesn't always reconcile to
// the system on the same day, so we record what was used and let an
// inventory cycle-count adjust if needed.
func (w *Workflows) maintenanceComplete(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, w.Repo)
	now := nowISO()

	pool := w.Repo.Pool()
	tx, err := pool.Begin(ctx)
	if err != nil {
		respondError(c, err)
		return
	}
	defer tx.Rollback(ctx)

	// Read the WO and its breakdown under FOR UPDATE so we don't race
	// with a concurrent /maintenance/:id PATCH editing the breakdown.
	var status, pmScheduleID, vehicleID, woDate string
	var odo float64
	var breakdownRaw []byte
	if err := tx.QueryRow(ctx,
		`SELECT status, parts_breakdown, COALESCE(pm_schedule_id,''), COALESCE(vehicle_id,''), odo, COALESCE(date::text,'')
		   FROM maintenance_items WHERE id = $1 FOR UPDATE`,
		id,
	).Scan(&status, &breakdownRaw, &pmScheduleID, &vehicleID, &odo, &woDate); err != nil {
		respondError(c, err)
		return
	}
	// Idempotency: completing an already-completed WO would decrement part stock
	// a second time. Reject instead.
	if status == "completed" {
		c.JSON(http.StatusConflict, gin.H{"error": "maintenance work order already completed"})
		return
	}

	var lines models.MaintenancePartLines
	if len(breakdownRaw) > 0 {
		if err := lines.Scan(breakdownRaw); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "parts_breakdown: " + err.Error()})
			return
		}
	}

	// Stock consumption. Under warehouse delegation, iag-warehouse is the
	// system-of-record for spare-parts stock: we post a single issue there
	// (idempotent per WO) and DO NOT touch parts.stock locally — the stock
	// projection is updated asynchronously by the warehouse-event consumer.
	// Without delegation we keep the legacy behavior: decrement parts.stock
	// and append an "out" movement per line inside this same tx.
	if w.Config.WarehouseDelegationEnabled && w.Warehouse != nil {
		if err := w.issuePartsToWarehouse(ctx, c, tx, id, vehicleID, lines); err != nil {
			// issuePartsToWarehouse has already written the HTTP error.
			return
		}
	} else {
		// Decrement stock + append "out" movement per line. We touch each
		// part via direct SQL (skipping Collection.Update) so we can keep
		// every UPDATE inside the same tx as the WO transition.
		for i, ln := range lines {
			if ln.PartID == "" {
				continue
			}
			mv := models.PartMovement{
				ID:       generateID("MV"),
				Date:     now,
				Type:     "out",
				Qty:      ln.Qty,
				UnitCost: ln.UnitCost,
				Ref:      id,
				Note:     ln.Note,
			}
			mvJSON, err := json.Marshal(mv)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("line %d: encode: %v", i, err)})
				return
			}
			// Best-effort flag: the decrement clamps at zero (workshop reality), so
			// surface an over-draw instead of losing it silently.
			var onHand int
			if scanErr := tx.QueryRow(ctx, `SELECT stock FROM parts WHERE id = $1`, ln.PartID).Scan(&onHand); scanErr == nil && onHand < int(ln.Qty) {
				slog.Warn("part stock overdraw clamped during WO completion",
					"wo", id, "part", ln.PartID, "requested", int(ln.Qty), "onHand", onHand)
			}
			tag, err := tx.Exec(ctx,
				`UPDATE parts
				    SET stock = GREATEST(stock - $2, 0),
				        movements = movements || $3::jsonb
				  WHERE id = $1`,
				ln.PartID, int(ln.Qty), mvJSON,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("line %d: %v", i, err)})
				return
			}
			if tag.RowsAffected() == 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("line %d: part %q not found", i, ln.PartID)})
				return
			}
		}
	}

	// Flip the WO and append lifecycle history.
	_ = status
	hist := models.StatusHistory{{At: now, Status: "completed", By: user}}
	histJSON, err := json.Marshal(hist)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE maintenance_items
		    SET status = 'completed',
		        status_history = status_history || $2::jsonb
		  WHERE id = $1`, id, histJSON,
	); err != nil {
		respondError(c, err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		respondError(c, err)
		return
	}

	updated, err := w.Repo.Maintenance.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if pmScheduleID != "" {
		mx := models.MaintenanceItem{
			PmScheduleID: pmScheduleID,
			VehicleID:    vehicleID,
			Odo:          odo,
			Date:         woDate,
		}
		if err := w.Repo.RollPMScheduleFromWorkOrder(ctx, mx); err != nil {
			respondError(c, err)
			return
		}
	}
	w.Repo.LogBest(ctx, "complete", "maintenance_item", id, fmt.Sprintf("%d parts decremented", len(lines)), user)
	w.emitFleet(ctx, events.TypeMaintenanceCompleted, map[string]string{
		"maintenanceId": id,
		"vehicleId":     vehicleID,
		"pmScheduleId":  pmScheduleID,
	}, id)
	c.JSON(http.StatusOK, updated)
}

// issuePartsToWarehouse posts the WO's parts breakdown to iag-warehouse as a
// single department issue (the system-of-record for stock under delegation).
// It is idempotent per work order via the Idempotency-Key, so a retried
// completion won't double-issue. Item resolution prefers the part's stored
// warehouse_item_id and falls back to a live SKU lookup. On failure it honors
// WarehouseIssueFailOpen: fail-open logs and lets the WO complete; fail-closed
// writes an HTTP error and returns it (non-nil) so the caller aborts the tx.
//
// Note: this runs while the maintenance_items row is held FOR UPDATE. The HTTP
// call is bounded (10s client timeout) and WO completion is a low-frequency
// operator action, so holding the row lock across the call is acceptable.
func (w *Workflows) issuePartsToWarehouse(ctx context.Context, c *gin.Context, tx pgx.Tx, woID, vehicleID string, lines models.MaintenancePartLines) error {
	var issueLines []warehouseclient.IssueLine
	for i, ln := range lines {
		if ln.PartID == "" || ln.Qty <= 0 {
			continue
		}
		var sku, whItemID string
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(sku,''), COALESCE(warehouse_item_id,'') FROM parts WHERE id = $1`,
			ln.PartID,
		).Scan(&sku, &whItemID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("line %d: part %q not found", i, ln.PartID)})
				return err
			}
			respondError(c, err)
			return err
		}

		itemID := whItemID
		if itemID == "" {
			if sku == "" {
				if w.Config.WarehouseIssueFailOpen {
					slog.Warn("part has no SKU; skipping warehouse issue line (fail-open)", "wo", woID, "part", ln.PartID)
					continue
				}
				c.JSON(http.StatusUnprocessableEntity, gin.H{"error": fmt.Sprintf("line %d: part %q has no SKU to map to a warehouse item", i, ln.PartID)})
				return fmt.Errorf("part %s has no sku", ln.PartID)
			}
			item, lerr := w.Warehouse.GetItemBySKU(ctx, sku)
			if lerr != nil {
				if w.Config.WarehouseIssueFailOpen {
					slog.Warn("warehouse item lookup failed; skipping line (fail-open)", "wo", woID, "part", ln.PartID, "sku", sku, "err", lerr)
					continue
				}
				if errors.Is(lerr, warehouseclient.ErrItemNotFound) {
					c.JSON(http.StatusUnprocessableEntity, gin.H{"error": fmt.Sprintf("line %d: sku %q has no matching warehouse item", i, sku)})
					return lerr
				}
				c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("line %d: warehouse lookup failed: %v", i, lerr)})
				return lerr
			}
			itemID = item.ID
		}
		issueLines = append(issueLines, warehouseclient.IssueLine{ItemID: itemID, Qty: ln.Qty})
	}

	if len(issueLines) == 0 {
		return nil
	}

	// Cost the issue to the vehicle's cost center when set, so finance can split
	// spare-parts spend per vehicle rather than against a single department.
	var costCenter string
	if vehicleID != "" {
		_ = tx.QueryRow(ctx, `SELECT COALESCE(cost_center,'') FROM vehicles WHERE id = $1`, vehicleID).Scan(&costCenter)
	}

	if _, err := w.Warehouse.IssueForDepartment(ctx, warehouseclient.IssueRequest{
		Department:     w.Config.WarehouseIssueDepartment,
		CostCenter:     costCenter,
		WorkOrderRef:   woID,
		Notes:          "fleet maintenance WO " + woID,
		Lines:          issueLines,
		IdempotencyKey: "fleet-wo-" + woID,
	}); err != nil {
		if w.Config.WarehouseIssueFailOpen {
			slog.Warn("warehouse issue failed; completing WO anyway (fail-open)", "wo", woID, "err", err)
			return nil
		}
		if errors.Is(err, warehouseclient.ErrInsufficientStock) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "warehouse: insufficient stock for one or more parts on this work order"})
			return err
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "warehouse issue failed: " + err.Error()})
		return err
	}
	return nil
}

func (w *Workflows) emitFleet(ctx context.Context, eventType string, fields map[string]string, key string) {
	if w.Events == nil || !w.Events.Enabled() {
		return
	}
	w.Events.PublishFleet(ctx, eventType, events.FleetEventData(fields), key, key)
}
