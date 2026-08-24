package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/config"
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
	cfg         config.Config
}

// NewFuelRequests wires the handler. records is the same *FuelRecords mounted
// at /fuel; fulfilment delegates record creation to it so validation,
// odometer checks, anomaly enrichment, and the finance event are not
// duplicated here. procurement is the optional iag-procurement client (nil when
// the integration is disabled) used to reconcile a request against the sourcing
// requisition procurement imports from the approval event.
func NewFuelRequests(repo *store.Repository, bus *events.Bus, records *FuelRecords, procurement *procurementclient.Client, cfg config.Config) *FuelRequests {
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
		cfg:         cfg,
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

	// Raise a fuel request anchored to a service (vehicle) request, inheriting
	// its assignment. Mounted on the parent group alongside the other
	// /requests/:id/* dispatch-chain workflows (assign, create-jmp, deploy).
	rg.POST("/requests/:id/request-fuel", add, f.createFromRequest)
}

// procurementAuthorizedStatuses are the terminals of procurement's requisition
// chain — the point at which every desk the request's value engaged has signed
// and the commitment is authorized.
//
// "Approved for Procurement" is the live terminal. The other two are terminals
// of earlier revisions of the same chain (migration 020 seeded a Finance desk
// stamping "Paid"; 021 renamed it "Payment Authorized" because nothing was
// being paid; 022 removed it), accepted so a requisition raised before those
// migrations is not stranded. None of them ever meant cash had moved.
var procurementAuthorizedStatuses = map[string]bool{
	"approved for procurement": true,
	"approved":                 true,
	"payment authorized":       true,
	"paid":                     true,
}

// commitmentAuthorized reports whether fulfilment may proceed, writing the HTTP
// error itself when it may not.
//
// Fleet's own approval authorizes the vehicle, driver and litres. It does not
// authorize the spend: procurement imports an approved fuel request as a
// sourcing requisition and walks it through PM -> Accounts Assistant -> GM
// (>= 5,000,000) -> CEO (>= 20,000,000), encumbering the budget. Without this
// check a caller holding add_fuel_record commits any amount without those desks.
//
// Deliberately NOT a payment check. Fuel is routinely bought on credit and the
// requisition chain disburses nothing; waiting for payment would ground
// vehicles over an invoice that is not due.
func (f *FuelRequests) commitmentAuthorized(c *gin.Context, id string) bool {
	if !f.cfg.FuelAuthorizationGateEnabled || f.procurement == nil {
		return true
	}

	reqn, err := f.procurement.GetRequisitionByOrigin(c.Request.Context(), id)
	if errors.Is(err, procurementclient.ErrRequisitionNotFound) {
		// No requisition for this request: either the bridge has not processed
		// the approval event yet, or this request predates the integration.
		// There is no chain to wait for, so fleet's approval is the only
		// authorization there is and it stands.
		return true
	}
	if err != nil {
		if f.cfg.FuelAuthorizationFailOpen {
			// A procurement outage must not stop a fleet taking fuel. The
			// bypass is recorded rather than silent.
			slog.Warn("procurement unreachable; allowing fuel fulfilment (fail-open)",
				"fuelRequest", id, "err", err)
			return true
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "cannot confirm procurement authorization for this request",
		})
		return false
	}

	if procurementAuthorizedStatuses[strings.ToLower(strings.TrimSpace(reqn.Status))] {
		return true
	}
	c.JSON(http.StatusConflict, gin.H{
		"error":              "procurement has not authorized this commitment yet",
		"requisitionId":      reqn.ID,
		"procurementStatus":  reqn.Status,
		"requiredAuthorized": "Approved for Procurement",
	})
	return false
}

// createFromRequestBody carries the fuel-specific fields the service request
// doesn't hold; vehicle/driver/requester/purpose are inherited from the request.
type createFromRequestBody struct {
	RequestedLitres float64 `json:"requestedLitres"`
	EstUnitPrice    float64 `json:"estUnitPrice"`
	Station         string  `json:"station"`
	Urgency         string  `json:"urgency"`
	Purpose         string  `json:"purpose"`
	Notes           string  `json:"notes"`
}

// createFromRequest raises a fuel request for a service request's assigned
// vehicle (mirrors POST /requests/:id/create-jmp). The request must have a
// vehicle assigned; the fuel request inherits it plus the driver/requester
// context and back-links via requestId (and the request's jmpId when present).
func (f *FuelRequests) createFromRequest(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	req, err := f.Repo.Requests.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if req.AssignedVehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "assign a vehicle before requesting fuel"})
		return
	}

	// Body is optional — fuel-specific fields default to zero/empty and are
	// validated by the shared create path (litres must be positive).
	var body createFromRequestBody
	_ = c.ShouldBindJSON(&body)

	purpose := req.Purpose
	if body.Purpose != "" {
		purpose = body.Purpose
	}
	rec := models.FuelRequest{
		VehicleID:       req.AssignedVehicleID,
		DriverID:        req.AssignedDriverID,
		RequesterName:   req.RequesterName,
		RequesterDept:   req.RequesterDept,
		RequestedLitres: body.RequestedLitres,
		EstUnitPrice:    body.EstUnitPrice,
		Station:         body.Station,
		Urgency:         body.Urgency,
		Purpose:         purpose,
		Notes:           body.Notes,
		RequestID:       req.ID,
		JmpID:           req.JmpID,
	}

	created, err := f.CreateRequest(c, &rec)
	if err != nil {
		respondMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, created)
}

// CreateRequest assigns an id, runs the standard create hooks (defaults, link
// enrichment, validation), persists, and audits — the shared path the generic
// POST and the from-request endpoint both go through, mirroring
// FuelRecords.CreateRecord.
func (f *FuelRequests) CreateRequest(c *gin.Context, rec *models.FuelRequest) (models.FuelRequest, error) {
	if rec.ID == "" {
		rec.ID = generateID(f.inner.IDPrefix)
	}
	if err := f.beforeCreate(c, rec); err != nil {
		return models.FuelRequest{}, err
	}
	created, err := f.inner.Collection.Add(c.Request.Context(), *rec)
	if err != nil {
		return models.FuelRequest{}, err
	}
	f.Repo.LogBest(c.Request.Context(), "create", f.inner.Entity, created.ID,
		"from-request:"+rec.RequestID, currentUser(c, f.Repo))
	return created, nil
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
	if rec.CreatedBy == "" {
		rec.CreatedBy = user
	}
	// Resolve the service-request / JMP link first so any inherited requester
	// wins over the fallback below.
	if err := f.linkToServiceRequest(c.Request.Context(), rec); err != nil {
		return err
	}
	if rec.RequesterName == "" {
		rec.RequesterName = user
	}
	return f.validate(c.Request.Context(), rec)
}

// linkToServiceRequest validates and enriches a fuel request from the service
// request / journey plan it references. Linking is OPTIONAL: a request with no
// requestId/jmpId is a standalone request against a vehicle and passes through
// unchanged. When a link IS given it must resolve, and the fuel request inherits
// the assigned vehicle/driver and requester/purpose context when those fields
// were left blank — so a fuel request raised "for request X" can't drift from
// X's assignment. A vehicle that contradicts the linked assignment is rejected.
func (f *FuelRequests) linkToServiceRequest(ctx context.Context, rec *models.FuelRequest) error {
	if rec.RequestID != "" {
		req, err := f.Repo.Requests.Get(ctx, rec.RequestID)
		if err != nil {
			return fmt.Errorf("%w: source request %q not found", errInvalidFuelRequest, rec.RequestID)
		}
		if rec.VehicleID == "" {
			rec.VehicleID = req.AssignedVehicleID
		}
		if rec.DriverID == "" {
			rec.DriverID = req.AssignedDriverID
		}
		if rec.RequesterName == "" {
			rec.RequesterName = req.RequesterName
		}
		if rec.RequesterDept == "" {
			rec.RequesterDept = req.RequesterDept
		}
		if rec.Purpose == "" {
			rec.Purpose = req.Purpose
		}
		if req.AssignedVehicleID != "" && rec.VehicleID != req.AssignedVehicleID {
			return fmt.Errorf("%w: vehicle %q does not match request %s assignment %q",
				errInvalidFuelRequest, rec.VehicleID, req.ID, req.AssignedVehicleID)
		}
		// Default the JMP link to the request's journey plan when it has one.
		if rec.JmpID == "" && req.JmpID != "" {
			rec.JmpID = req.JmpID
		}
	}
	if rec.JmpID != "" {
		jmp, err := f.Repo.JMPs.Get(ctx, rec.JmpID)
		if err != nil {
			return fmt.Errorf("%w: source journey plan %q not found", errInvalidFuelRequest, rec.JmpID)
		}
		if rec.VehicleID == "" {
			rec.VehicleID = jmp.VehicleID
		}
		if rec.DriverID == "" {
			rec.DriverID = jmp.DriverID
		}
		if jmp.VehicleID != "" && rec.VehicleID != jmp.VehicleID {
			return fmt.Errorf("%w: vehicle %q does not match journey plan %s vehicle %q",
				errInvalidFuelRequest, rec.VehicleID, jmp.ID, jmp.VehicleID)
		}
	}
	return nil
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
	f.notifyFuelDecision(ctx, updated)
	c.JSON(http.StatusOK, updated)
}

// notifyFuelDecision emails the requester the outcome of their fuel request.
//
// The domain event emitted alongside this (fleet.fuel_request.approved) exists
// for procurement's sourcing bridge, not for people — so before this, a
// requester learned nothing either way. fleet.alert.raised only fires from the
// signal scanner at "crit" severity, which an approval never reaches.
//
// CreatedBy is a user id, so the address comes from the notifications store;
// when it cannot be resolved the fleet desk stands in, with the requester
// named in the body. Best effort — the decision is committed either way.
func (f *FuelRequests) notifyFuelDecision(ctx context.Context, req models.FuelRequest) {
	if f.Events == nil || !f.Events.Enabled() {
		return
	}
	// A resolved requester is a specific person and carries NO audience: an
	// audience resolves to whoever an administrator has put on the fleet desk,
	// which would redirect a personal notification away from the person it is
	// about. Only the desk fallback is routable.
	recipient := ""
	audience := ""
	if req.CreatedBy != "" && f.Repo != nil && f.Repo.Notifications != nil {
		if email, err := f.Repo.Notifications.RecipientEmail(ctx, req.CreatedBy); err == nil {
			recipient = strings.TrimSpace(email)
		}
	}
	if recipient == "" {
		recipient = events.DefaultNotifyRecipient()
		audience = "approvals.fleet"
	}
	outcome := req.Status // "approved" or "rejected"
	body := fmt.Sprintf("Fuel request %s for %.2f litres (%s) raised by %s was %s.",
		req.ID, req.RequestedLitres, req.Station, req.RequesterName, outcome)
	if strings.TrimSpace(req.ReviewerNotes) != "" {
		body += " Reason: " + req.ReviewerNotes
	}
	f.Events.PublishFleetAlertTo(ctx, "email", audience, recipient, "approval.decision", map[string]string{
		"Title": "Fuel request " + outcome + ": " + req.ID,
		"Body":  body,
	})
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
	if !f.commitmentAuthorized(c, req.ID) {
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
		// Chain linkage so consumers (and the audit trail) can tie the spend
		// back to the vehicle request / journey plan it was raised against.
		"serviceRequestId": req.RequestID,
		"jmpId":            req.JmpID,
	}), req.ID, req.ID)
}
