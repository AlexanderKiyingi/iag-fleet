package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/procurementclient"
	"github.com/iag/fleet-tool/backend/internal/store"
)

var errInvalidFuelRequest = errors.New("invalid fuel request")

// FuelRequests is CRUD for fuel_requests plus the approval lifecycle:
// approve / reject, cancel, and fulfil (which spawns a fuel_record via the
// FuelRecords handler so the finance event and anomaly enrichment stay in one
// place). Modelled on the FuelRecords handler — it owns a generic Resource for
// CRUD and layers the workflow transitions on the same route group.
type FuelRequests struct {
	inner       Resource[models.FuelRequest, *models.FuelRequest]
	Repo        *store.Repository
	Events      *events.Bus
	records     *FuelRecords
	procurement *procurementclient.Client
}

// NewFuelRequests wires the handler. records is the same *FuelRecords mounted
// at /fuel; fulfilment delegates record creation to it so validation,
// odometer checks, anomaly enrichment, and the finance event are not
// duplicated here. procurement is the optional iag-procurement client (nil when
// the integration is disabled) used to reconcile a request against the sourcing
// requisition procurement imports from the approval event.
func NewFuelRequests(repo *store.Repository, bus *events.Bus, records *FuelRecords, procurement *procurementclient.Client) *FuelRequests {
	f := &FuelRequests{
		inner: Resource[models.FuelRequest, *models.FuelRequest]{
			Repo:       repo,
			Collection: repo.FuelRequests,
			Entity:     "fuel_request",
			IDPrefix:   "FREQ",
		},
		Repo:        repo,
		Events:      bus,
		records:     records,
		procurement: procurement,
	}
	f.inner.BeforeCreate = f.beforeCreate
	f.inner.BeforeUpdate = f.beforeUpdate
	return f
}

func (f *FuelRequests) Register(rg *gin.RouterGroup) {
	g := rg.Group("/fuel-requests")
	e := f.inner.Entity
	view := auth.RequirePerm("view_" + e)
	add := auth.RequirePerm("add_" + e)
	change := auth.RequirePerm("change_" + e)
	del := auth.RequirePerm("delete_" + e)

	g.GET("", view, f.inner.list)
	g.GET("/search", view, f.inner.search)
	g.GET("/:id", view, f.get)
	g.POST("", add, f.inner.create)
	g.POST("/bulk", add, f.inner.bulkCreate)
	g.PUT("/:id", change, f.inner.replace)
	g.PATCH("/:id", change, f.inner.patch)
	g.PATCH("/bulk", change, f.inner.bulkPatch)
	g.DELETE("/:id", del, f.inner.remove)
	g.DELETE("/bulk", del, f.inner.bulkDelete)

	// Lifecycle transitions.
	g.POST("/:id/approve", auth.RequirePerm("approve_fuel_request"), f.approve)
	g.POST("/:id/cancel", change, f.cancel)
	// Fulfilment writes a fuel_record, so it's gated on add_fuel_record —
	// the same permission a direct POST /fuel requires.
	g.POST("/:id/fulfill", auth.RequirePerm("add_fuel_record"), f.fulfill)
}

// get returns one fuel request. When the procurement integration is enabled it
// best-effort enriches the response with the sourcing requisition procurement
// imported for this request (origin_ref = request id), so the detail view can
// show procurement's approval state. A missing or unreachable procurement is
// non-fatal — the request is returned without the procurement fields.
func (f *FuelRequests) get(c *gin.Context) {
	id := c.Param("id")
	rec, err := f.Repo.FuelRequests.Get(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	if f.procurement != nil {
		if req, perr := f.procurement.GetRequisitionByOrigin(c.Request.Context(), id); perr == nil {
			rec.ProcurementRequisitionID = req.ID
			rec.ProcurementStatus = req.Status
		}
	}
	c.JSON(http.StatusOK, rec)
}

func (f *FuelRequests) beforeCreate(c *gin.Context, rec *models.FuelRequest) error {
	user := currentUser(c, f.Repo)
	if rec.Status == "" {
		rec.Status = "submitted"
	}
	if rec.SubmittedAt == "" {
		rec.SubmittedAt = nowISO()
	}
	if rec.RequesterName == "" {
		rec.RequesterName = user
	}
	if rec.CreatedBy == "" {
		rec.CreatedBy = user
	}
	return f.validate(c.Request.Context(), rec)
}

// beforeUpdate guards direct PATCH/PUT edits. Status transitions must go
// through the lifecycle endpoints; a raw status edit is rejected so the
// approver/fulfilment bookkeeping (approvedBy, fuelRecordId) can't be bypassed.
func (f *FuelRequests) beforeUpdate(c *gin.Context, rec *models.FuelRequest) error {
	return f.validate(c.Request.Context(), rec)
}

func (f *FuelRequests) validate(ctx context.Context, rec *models.FuelRequest) error {
	if rec.RequestedLitres <= 0 {
		return fmt.Errorf("%w: requestedLitres must be positive", errInvalidFuelRequest)
	}
	if rec.EstUnitPrice < 0 || rec.EstTotal < 0 {
		return fmt.Errorf("%w: estimates must be non-negative", errInvalidFuelRequest)
	}
	if rec.EstTotal == 0 && rec.EstUnitPrice > 0 {
		rec.EstTotal = rec.RequestedLitres * rec.EstUnitPrice
	}
	if !validFuelRequestStatus(rec.Status) {
		return fmt.Errorf("%w: unknown status %q", errInvalidFuelRequest, rec.Status)
	}
	return validateVehicleExists(ctx, f.Repo, rec.VehicleID)
}

func validFuelRequestStatus(s string) bool {
	for _, v := range models.FuelRequestStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// ─────────────────────────── approve / reject ───────────────────────────

type fuelApproveBody struct {
	Approved bool   `json:"approved"`
	Notes    string `json:"notes"`
}

func (f *FuelRequests) approve(c *gin.Context) {
	var body fuelApproveBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if declineReasonMissing(c, body.Approved, body.Notes) {
		return
	}
	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, f.Repo)

	var blocked string
	updated, err := f.Repo.FuelRequests.Update(ctx, id, func(r *models.FuelRequest) {
		if r.Status != "submitted" {
			blocked = r.Status
			return
		}
		if body.Approved {
			r.Status = "approved"
		} else {
			r.Status = "rejected"
		}
		r.ApprovedBy = user
		r.ApprovedAt = nowISO()
		r.ReviewerNotes = body.Notes
	})
	if err != nil {
		respondError(c, err)
		return
	}
	if blocked != "" {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "only submitted requests can be approved or rejected",
			"status": blocked,
		})
		return
	}

	action := "approve"
	if !body.Approved {
		action = "reject"
	}
	f.Repo.LogBest(ctx, action, "fuel_request", id, body.Notes, user)
	f.emitApproved(ctx, updated, user)
	c.JSON(http.StatusOK, updated)
}

// ─────────────────────────────── cancel ─────────────────────────────────

func (f *FuelRequests) cancel(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, f.Repo)

	var blocked string
	updated, err := f.Repo.FuelRequests.Update(ctx, id, func(r *models.FuelRequest) {
		// Fulfilled requests are terminal — a fuel_record already exists.
		if r.Status == "fulfilled" || r.Status == "cancelled" {
			blocked = r.Status
			return
		}
		r.Status = "cancelled"
	})
	if err != nil {
		respondError(c, err)
		return
	}
	if blocked != "" {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "request cannot be cancelled in its current state",
			"status": blocked,
		})
		return
	}
	f.Repo.LogBest(ctx, "cancel", "fuel_request", id, "", user)
	c.JSON(http.StatusOK, updated)
}

// ────────────────────────────── fulfil ──────────────────────────────────

// fuelFulfillBody carries the actuals captured at the pump. Every field is
// optional; absent fields fall back to the request's planned values (and the
// current date). Total is always derived from litres × unitPrice so it passes
// fuel-record validation.
type fuelFulfillBody struct {
	Litres    float64 `json:"litres"`
	UnitPrice float64 `json:"unitPrice"`
	Odo       float64 `json:"odo"`
	Station   string  `json:"station"`
	Invoice   string  `json:"invoice"`
	Date      string  `json:"date"`
}

func (f *FuelRequests) fulfill(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	req, err := f.Repo.FuelRequests.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if req.Status != "approved" {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "only approved requests can be fulfilled",
			"status": req.Status,
		})
		return
	}
	if req.FuelRecordID != "" {
		c.JSON(http.StatusConflict, gin.H{
			"error":        "request already fulfilled",
			"fuelRecordId": req.FuelRecordID,
		})
		return
	}

	// Body is optional — a fulfilment with no overrides uses the planned values.
	var body fuelFulfillBody
	_ = c.ShouldBindJSON(&body)

	litres := req.RequestedLitres
	if body.Litres > 0 {
		litres = body.Litres
	}
	unitPrice := req.EstUnitPrice
	if body.UnitPrice > 0 {
		unitPrice = body.UnitPrice
	}
	station := req.Station
	if body.Station != "" {
		station = body.Station
	}
	date := body.Date
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}

	rec := models.FuelRecord{
		VehicleID: req.VehicleID,
		DriverID:  req.DriverID,
		Date:      date,
		Litres:    litres,
		UnitPrice: unitPrice,
		Total:     litres * unitPrice,
		Station:   station,
		Invoice:   body.Invoice,
		Odo:       body.Odo,
		Notes:     "Fulfils fuel request " + id,
	}
	created, err := f.records.CreateRecord(c, &rec)
	if err != nil {
		respondMutationError(c, err)
		return
	}

	user := currentUser(c, f.Repo)
	updated, err := f.Repo.FuelRequests.Update(ctx, id, func(r *models.FuelRequest) {
		r.Status = "fulfilled"
		r.FuelRecordID = created.ID
	})
	if err != nil {
		// The fuel record was created but the link write failed. Surface the
		// record id so the caller can reconcile rather than silently lose it.
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":        err.Error(),
			"fuelRecordId": created.ID,
		})
		return
	}
	f.Repo.LogBest(ctx, "fulfill", "fuel_request", id, created.ID, user)
	c.JSON(http.StatusOK, gin.H{"request": updated, "fuelRecord": created})
}

func (f *FuelRequests) emitApproved(ctx context.Context, req models.FuelRequest, user string) {
	if f.Events == nil || !f.Events.Enabled() {
		return
	}
	// Payload is enriched (litres/estTotal/currency/station/requester/dept) so a
	// downstream consumer — procurement's fleet-fuel bridge — can turn an
	// approved fuel request into a sourcing requisition without a callback.
	// Numbers are formatted as strings to match the fleet.fuel.recorded shape.
	f.Events.PublishFleet(ctx, events.TypeFuelRequestApproved, events.FleetEventData(map[string]string{
		"requestId":  req.ID,
		"vehicleId":  req.VehicleID,
		"status":     req.Status,
		"approvedBy": user,
		"litres":     fmt.Sprintf("%.2f", req.RequestedLitres),
		"estTotal":   fmt.Sprintf("%.2f", req.EstTotal),
		"currency":   envOr("FLEET_FUEL_CURRENCY", "UGX"),
		"station":    req.Station,
		"requester":  req.RequesterName,
		"dept":       req.RequesterDept,
		"purpose":    req.Purpose,
	}), req.ID, req.ID)
}
