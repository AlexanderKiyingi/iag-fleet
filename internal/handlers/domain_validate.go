package handlers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

var (
	errDriverPermitInvalid       = errors.New("driver permit expired or missing")
	errInvalidPMSchedule         = errors.New("invalid PM schedule")
	errInvalidMaintenanceStatus  = errors.New("invalid maintenance status")
	errInvalidComplianceDoc      = errors.New("invalid compliance document")
	errInvalidComplianceExpiry   = errors.New("expiry must be today or in the future")
	errDriverDoubleBooked        = errors.New("driver already has an overlapping journey in this period")
	errVehicleDoubleBooked       = errors.New("vehicle already has an overlapping journey in this period")
	errDriverAlreadyOnVehicle    = errors.New("driver is already assigned to another vehicle")
	errDriverEligibility         = errors.New("driver not eligible for dispatch")
	errVehicleNotDispatchable    = errors.New("vehicle not dispatchable")
	errInvalidFuelRecord         = errors.New("invalid fuel record")
	errTripRefsRequired          = errors.New("trip reference required")
	errAuthorisationRefsRequired = errors.New("authorisation reference required")
	errPermitNotAuthorised       = errors.New("driver not authorised for this vehicle category")
	errToolboxIncomplete         = errors.New("complete the toolbox talk before activating the journey")
	errVehicleNotFound           = errors.New("vehicle not found")
	errVehicleInUse              = errors.New("vehicle is referenced by a live journey")
	errDriverInUse               = errors.New("driver is referenced by a live journey or vehicle")
	errTyrePositionTaken         = errors.New("a tyre is already mounted at this position")
	errInvalidTyre               = errors.New("invalid tyre")
	errInvalidInspectionTemplate = errors.New("invalid inspection template")
)

// inspectionTemplateKinds mirrors the CHECK constraint on
// inspection_templates.kind (migration 0009). Kept next to the validator so the
// two are changed together; drifting from the constraint just moves the failure
// back into SQL, which is what this file exists to prevent.
var inspectionTemplateKinds = []string{"pre-trip", "post-trip", "periodic"}

func containsString(list []string, v string) bool {
	return slices.Contains(list, v)
}

func parseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(s))
}

func validateDriver(d *models.Driver) error {
	if d == nil {
		return nil
	}
	if d.FirstAid && strings.TrimSpace(d.FirstAidExpiry) == "" {
		return fmt.Errorf("firstAidExpiry required when firstAid is true")
	}
	if d.Defensive && strings.TrimSpace(d.DefensiveExpiry) == "" {
		return fmt.Errorf("defensiveExpiry required when defensive is true")
	}
	if d.PermitExpiry != "" {
		if _, err := parseDate(d.PermitExpiry); err != nil {
			return fmt.Errorf("permitExpiry: invalid date")
		}
	}
	return nil
}

func validateDriverDispatch(ctx context.Context, repo *store.Repository, driverID string) error {
	if driverID == "" {
		return errDriverPermitInvalid
	}
	drv, err := repo.Drivers.Get(ctx, driverID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errDriverNotFound
		}
		return err
	}
	now := time.Now().UTC()
	if !store.DriverPermitOK(drv, now) {
		return errDriverPermitInvalid
	}
	return driverCertsOK(drv, now)
}

// driverCertsOK rejects dispatch when a required certificate has expired:
// medical (whenever an expiry is recorded), and first-aid / defensive when the
// driver carries them. An unset expiry for a non-required cert is allowed.
func driverCertsOK(d models.Driver, now time.Time) error {
	today := now.Truncate(24 * time.Hour)
	expired := func(expiry string) bool {
		s := strings.TrimSpace(expiry)
		if s == "" {
			return false
		}
		t, err := parseDate(s)
		if err != nil {
			return false
		}
		return t.Before(today)
	}
	if expired(d.MedicalExpiry) {
		return fmt.Errorf("%w: medical certificate expired", errDriverEligibility)
	}
	if d.FirstAid && expired(d.FirstAidExpiry) {
		return fmt.Errorf("%w: first-aid certificate expired", errDriverEligibility)
	}
	if d.Defensive && expired(d.DefensiveExpiry) {
		return fmt.Errorf("%w: defensive-driving certificate expired", errDriverEligibility)
	}
	return nil
}

// vehicleDispatchable rejects vehicles that are out of service. Empty/unknown
// status or mechStatus is allowed (don't block on missing data).
func vehicleDispatchable(v models.Vehicle) error {
	switch v.Status {
	case "offline", "maintenance":
		return fmt.Errorf("%w: status=%s", errVehicleNotDispatchable, v.Status)
	}
	switch v.MechStatus {
	case "grounded", "out-of-service":
		return fmt.Errorf("%w: mechStatus=%s", errVehicleNotDispatchable, v.MechStatus)
	}
	return nil
}

// validateVehicleDispatchable looks the vehicle up and checks it is dispatchable.
// A non-existent vehicle is not rejected here (referential existence is a
// separate concern); only a found-but-out-of-service vehicle is blocked.
func validateVehicleDispatchable(ctx context.Context, repo *store.Repository, vehicleID string) error {
	if vehicleID == "" {
		return nil
	}
	v, err := repo.Vehicles.Get(ctx, vehicleID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	return vehicleDispatchable(v)
}

// validateFuelRecord enforces fuel-record sanity: non-negative quantities, an
// internally consistent total (total ≈ litres × unitPrice within tolerance), and
// odometer monotonicity in date order for the vehicle. excludeID skips the record
// being updated in place.
// validateFuelValues is the DB-free part: non-negative quantities and an
// internally consistent total. Used directly on the bulk path to avoid a
// full-table read per row.
func validateFuelValues(rec *models.FuelRecord) error {
	if rec.Litres < 0 || rec.UnitPrice < 0 || rec.Total < 0 {
		return fmt.Errorf("%w: litres, unitPrice and total must be non-negative", errInvalidFuelRecord)
	}
	if rec.Litres > 0 && rec.UnitPrice > 0 {
		expected := rec.Litres * rec.UnitPrice
		if math.Abs(rec.Total-expected) > expected*0.02+1 { // 2% + rounding tolerance
			return fmt.Errorf("%w: total %.2f inconsistent with litres×unitPrice %.2f", errInvalidFuelRecord, rec.Total, expected)
		}
	}
	return nil
}

func validateFuelRecord(ctx context.Context, repo *store.Repository, rec *models.FuelRecord, excludeID string) error {
	if err := validateFuelValues(rec); err != nil {
		return err
	}
	if err := validateVehicleExists(ctx, repo, rec.VehicleID); err != nil {
		return err
	}
	if rec.Odo <= 0 || rec.VehicleID == "" {
		return nil
	}
	recDate, err := parseDate(rec.Date)
	if err != nil {
		return nil // unparseable date — skip the temporal check
	}
	all, err := repo.Fuel.List(ctx)
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.ID == excludeID || o.VehicleID != rec.VehicleID || o.Odo <= 0 {
			continue
		}
		od, e := parseDate(o.Date)
		if e != nil {
			continue
		}
		if od.Before(recDate) && o.Odo > rec.Odo {
			return fmt.Errorf("%w: odo %.0f is below an earlier reading %.0f on %s", errInvalidFuelRecord, rec.Odo, o.Odo, o.Date)
		}
		if od.After(recDate) && o.Odo < rec.Odo {
			return fmt.Errorf("%w: odo %.0f is above a later reading %.0f on %s", errInvalidFuelRecord, rec.Odo, o.Odo, o.Date)
		}
	}
	return nil
}

// jmpDateWindow returns the [start, end] dates for a journey. end defaults to
// start when expectedReturn is empty/unparseable. The window is day-granular —
// JMP/request dates are DATE strings with no time-of-day component.
func jmpDateWindow(startDate, expectedReturn string) (time.Time, time.Time, bool) {
	s, err := parseDate(startDate)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	e := s
	if r := strings.TrimSpace(expectedReturn); r != "" {
		if pe, err := parseDate(r); err == nil {
			e = pe
		}
	}
	if e.Before(s) {
		e = s
	}
	return s, e, true
}

func dateRangesOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return !aStart.After(bEnd) && !bStart.After(aEnd)
}

// jmpIsLive reports whether a journey still occupies its driver/vehicle.
// Completed and cancelled journeys free up the assignment.
func jmpIsLive(status string) bool {
	return status != "completed" && status != "cancelled"
}

// validateJMPAvailability rejects committing driverID/vehicleID to a journey
// whose [startDate, expectedReturn] window overlaps another LIVE JMP that already
// uses the same driver or the same vehicle. excludeID skips the JMP being
// updated in place. This enforces: a driver can't be on two journeys at once,
// and a vehicle can't be booked for two journeys in the same period.
func validateJMPAvailability(ctx context.Context, repo *store.Repository, driverID, vehicleID, startDate, expectedReturn, excludeID string) error {
	if driverID == "" && vehicleID == "" {
		return nil
	}
	s, e, ok := jmpDateWindow(startDate, expectedReturn)
	if !ok {
		return nil // unparseable dates — leave to other validation/normalisation
	}
	jmps, err := repo.JMPs.List(ctx)
	if err != nil {
		return err
	}
	for _, j := range jmps {
		if j.ID == excludeID || !jmpIsLive(j.Status) {
			continue
		}
		js, je, ok := jmpDateWindow(j.StartDate, j.ExpectedReturn)
		if !ok || !dateRangesOverlap(s, e, js, je) {
			continue
		}
		if driverID != "" && j.DriverID == driverID {
			return fmt.Errorf("%w (conflicts with %s)", errDriverDoubleBooked, j.ID)
		}
		if vehicleID != "" && j.VehicleID == vehicleID {
			return fmt.Errorf("%w (conflicts with %s)", errVehicleDoubleBooked, j.ID)
		}
	}
	return nil
}

// validateRequestAssignment re-runs the dispatch guards for a service request's
// assigned vehicle/driver, so they can't be set via a generic PATCH that bypasses
// the /assign workflow: the vehicle must be dispatchable, the driver eligible,
// and neither already committed to an overlapping journey in the request window.
// The JMP derived from THIS request (sourceRequestId == req.ID) is excluded, so
// re-validating an unchanged assignment after its journey exists is not a false
// conflict.
func validateRequestAssignment(ctx context.Context, repo *store.Repository, req *models.ServiceRequest) error {
	if req.AssignedVehicleID == "" && req.AssignedDriverID == "" {
		return nil
	}
	if err := validateVehicleDispatchable(ctx, repo, req.AssignedVehicleID); err != nil {
		return err
	}
	if req.AssignedDriverID != "" {
		if err := validateDriverDispatch(ctx, repo, req.AssignedDriverID); err != nil {
			return err
		}
		if err := validateDriverVehicleAuthorisation(
			ctx, repo, req.AssignedDriverID, req.AssignedVehicleID); err != nil {
			return err
		}
	}
	s, e, ok := jmpDateWindow(req.StartDate, req.EndDate)
	if !ok {
		return nil
	}
	jmps, err := repo.JMPs.List(ctx)
	if err != nil {
		return err
	}
	for _, j := range jmps {
		if !jmpIsLive(j.Status) || (req.ID != "" && j.SourceRequestID == req.ID) {
			continue
		}
		js, je, ok := jmpDateWindow(j.StartDate, j.ExpectedReturn)
		if !ok || !dateRangesOverlap(s, e, js, je) {
			continue
		}
		if req.AssignedDriverID != "" && j.DriverID == req.AssignedDriverID {
			return fmt.Errorf("%w (conflicts with %s)", errDriverDoubleBooked, j.ID)
		}
		if req.AssignedVehicleID != "" && j.VehicleID == req.AssignedVehicleID {
			return fmt.Errorf("%w (conflicts with %s)", errVehicleDoubleBooked, j.ID)
		}
	}
	return nil
}

// validateVehicleExists / validateDriverExists enforce referential integrity on
// assignment: a record may not reference a vehicle/driver that doesn't exist.
// (The DB has no FK on these columns.)
func validateVehicleExists(ctx context.Context, repo *store.Repository, vehicleID string) error {
	if vehicleID == "" {
		return nil
	}
	if _, err := repo.Vehicles.Get(ctx, vehicleID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errVehicleNotFound
		}
		return err
	}
	return nil
}

func validateDriverExists(ctx context.Context, repo *store.Repository, driverID string) error {
	if driverID == "" {
		return nil
	}
	if _, err := repo.Drivers.Get(ctx, driverID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errDriverNotFound
		}
		return err
	}
	return nil
}

// liveJMPReference returns the id of a live JMP that references the given
// vehicle or driver, if any.
func liveJMPReference(ctx context.Context, repo *store.Repository, vehicleID, driverID string) (string, bool, error) {
	jmps, err := repo.JMPs.List(ctx)
	if err != nil {
		return "", false, err
	}
	for _, j := range jmps {
		if !jmpIsLive(j.Status) {
			continue
		}
		if vehicleID != "" && j.VehicleID == vehicleID {
			return j.ID, true, nil
		}
		if driverID != "" && j.DriverID == driverID {
			return j.ID, true, nil
		}
	}
	return "", false, nil
}

// validateVehicleDeletable blocks deleting a vehicle still referenced by a live
// journey (which would dangle the reference).
func validateVehicleDeletable(ctx context.Context, repo *store.Repository, vehicleID string) error {
	jid, ref, err := liveJMPReference(ctx, repo, vehicleID, "")
	if err != nil {
		return err
	}
	if ref {
		return fmt.Errorf("%w (%s)", errVehicleInUse, jid)
	}
	return nil
}

// validateDriverDeletable blocks deleting a driver still referenced by a live
// journey or assigned as a vehicle's driver.
func validateDriverDeletable(ctx context.Context, repo *store.Repository, driverID string) error {
	jid, ref, err := liveJMPReference(ctx, repo, "", driverID)
	if err != nil {
		return err
	}
	if ref {
		return fmt.Errorf("%w (live journey %s)", errDriverInUse, jid)
	}
	vehicles, err := repo.Vehicles.List(ctx)
	if err != nil {
		return err
	}
	for _, v := range vehicles {
		if v.DriverID == driverID {
			return fmt.Errorf("%w (vehicle %s)", errDriverInUse, v.ID)
		}
	}
	return nil
}

func isRetiredTyre(status string) bool {
	switch status {
	case "replaced", "retired", "scrapped", "removed":
		return true
	}
	return false
}

// validateTyre checks the fields tyres declares NOT NULL (migration 0001)
// before the row reaches Postgres.
//
// Without this a create missing any of them came back as 500/502 carrying the
// raw driver text — `null value in column "mounted_date" of relation "tyres"
// violates not-null constraint (SQLSTATE 23502)` — which the web app then shows
// to whoever was filling the form. The constraint is right; letting it be the
// first thing that notices is not.
//
// vehicleId is checked here rather than relying on validateVehicleExists, which
// treats "" as "not specified" and returns nil. That is correct for the entities
// where a vehicle is optional, and wrong for a tyre, which is mounted on one by
// definition.
//
// Tread depths are only range-checked, not required: 0 is a legitimate reading
// and the column has no default, so "absent" and "worn flat" are the same value
// here and cannot be told apart.
func validateTyre(t *models.Tyre) error {
	if t == nil {
		return nil
	}
	for _, f := range []struct {
		name  string
		value string
	}{
		{"vehicleId", t.VehicleID},
		{"position", t.Position},
		{"brand", t.Brand},
		{"model", t.Model},
		{"serial", t.Serial},
		{"mountedDate", t.MountedDate},
		{"status", t.Status},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%w: %s is required", errInvalidTyre, f.name)
		}
	}
	if _, err := parseDate(t.MountedDate); err != nil {
		return fmt.Errorf("%w: mountedDate must be YYYY-MM-DD", errInvalidTyre)
	}
	if t.TreadDepthMm < 0 || t.TreadInitialMm < 0 {
		return fmt.Errorf("%w: tread depths must be non-negative", errInvalidTyre)
	}
	return nil
}

// validateInspectionTemplate checks name and kind before the row reaches
// Postgres.
//
// kind carries a CHECK constraint (migration 0009) listing the three valid
// values. A create without it sent the empty string, the constraint rejected it,
// and the caller got a 500/502 quoting the constraint name — which names the
// database object rather than the field the operator left blank. Answering here
// means a 400 that says which values are allowed.
func validateInspectionTemplate(t *models.InspectionTemplate) error {
	if t == nil {
		return nil
	}
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("%w: name is required", errInvalidInspectionTemplate)
	}
	kind := strings.TrimSpace(t.Kind)
	if !containsString(inspectionTemplateKinds, kind) {
		return fmt.Errorf(
			"%w: kind must be one of %s (got %q)",
			errInvalidInspectionTemplate, strings.Join(inspectionTemplateKinds, ", "), kind,
		)
	}
	return nil
}

// validateTyrePosition enforces one current tyre per (vehicle, position):
// retired tyres are kept for history and don't block a fresh mount.
func validateTyrePosition(ctx context.Context, repo *store.Repository, t *models.Tyre) error {
	if t.VehicleID == "" || t.Position == "" || isRetiredTyre(t.Status) {
		return nil
	}
	all, err := repo.Tyres.List(ctx)
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.ID == t.ID || o.VehicleID != t.VehicleID || o.Position != t.Position || isRetiredTyre(o.Status) {
			continue
		}
		return fmt.Errorf("%w: %s %s (tyre %s)", errTyrePositionTaken, t.VehicleID, t.Position, o.ID)
	}
	return nil
}

// validateJMPRefs checks a journey's driver/vehicle exist and the vehicle is
// dispatchable.
func validateJMPRefs(ctx context.Context, repo *store.Repository, driverID, vehicleID string) error {
	if err := validateDriverExists(ctx, repo, driverID); err != nil {
		return err
	}
	if err := validateVehicleExists(ctx, repo, vehicleID); err != nil {
		return err
	}
	return validateVehicleDispatchable(ctx, repo, vehicleID)
}

// requireToolboxForActive blocks activating a journey before the pre-trip
// toolbox talk is completed — re-securing the complete-toolbox workflow gate
// against a generic PATCH/PUT that sets status="active" directly.
func requireToolboxForActive(j *models.JMP) error {
	if j.Status == "active" && !j.Toolbox.Completed {
		return errToolboxIncomplete
	}
	return nil
}

// validateDriverNotOnAnotherVehicle enforces one driver per vehicle at the roster
// level: a driver may be the assigned driver of at most one vehicle at a time.
// excludeVehicleID skips the vehicle being updated in place.
func validateDriverNotOnAnotherVehicle(ctx context.Context, repo *store.Repository, driverID, excludeVehicleID string) error {
	if driverID == "" {
		return nil
	}
	vehicles, err := repo.Vehicles.List(ctx)
	if err != nil {
		return err
	}
	for _, v := range vehicles {
		if v.ID != excludeVehicleID && v.DriverID == driverID {
			return fmt.Errorf("%w (%s)", errDriverAlreadyOnVehicle, v.ID)
		}
	}
	return nil
}

func validatePMSchedule(ctx context.Context, repo *store.Repository, s *models.PMSchedule) error {
	if s == nil {
		return nil
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: name required", errInvalidPMSchedule)
	}
	if !containsString(models.PMServiceTypes, s.ServiceType) {
		return fmt.Errorf("%w: unknown serviceType %q", errInvalidPMSchedule, s.ServiceType)
	}
	hasKm := s.IntervalKm != nil && *s.IntervalKm > 0
	hasDays := s.IntervalDays != nil && *s.IntervalDays > 0
	if !hasKm && !hasDays {
		return fmt.Errorf("%w: intervalKm or intervalDays required", errInvalidPMSchedule)
	}
	if s.IntervalKm != nil && *s.IntervalKm <= 0 {
		return fmt.Errorf("%w: intervalKm must be positive", errInvalidPMSchedule)
	}
	if s.IntervalDays != nil && *s.IntervalDays <= 0 {
		return fmt.Errorf("%w: intervalDays must be positive", errInvalidPMSchedule)
	}
	if s.VehicleID != "" {
		if _, err := repo.Vehicles.Get(ctx, s.VehicleID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("%w: vehicle not found", errInvalidPMSchedule)
			}
			return err
		}
	}
	return nil
}

func validateMaintenanceStatus(status string) error {
	if !containsString(models.MaintenanceStatuses, status) {
		return fmt.Errorf("%w: %q", errInvalidMaintenanceStatus, status)
	}
	return nil
}

func validateComplianceItem(ci *models.ComplianceItem) error {
	if ci == nil {
		return nil
	}
	if ci.DriverID == "" && ci.VehicleID == "" {
		return fmt.Errorf("%w: driverId or vehicleId required", errInvalidComplianceDoc)
	}
	if !containsString(models.ComplianceDocTypes, ci.DocType) {
		return fmt.Errorf("%w: unknown docType %q", errInvalidComplianceDoc, ci.DocType)
	}
	if ci.Expiry != "" {
		if _, err := parseDate(ci.Expiry); err != nil {
			return fmt.Errorf("%w: invalid expiry date", errInvalidComplianceDoc)
		}
	}
	if ci.Status != "" && !containsString(models.ComplianceStatuses, ci.Status) {
		return fmt.Errorf("%w: unknown status %q", errInvalidComplianceDoc, ci.Status)
	}
	return nil
}

func validateFutureExpiry(expiry string) error {
	if expiry == "" {
		return errInvalidComplianceExpiry
	}
	d, err := parseDate(expiry)
	if err != nil {
		return errInvalidComplianceExpiry
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if d.Before(today) {
		return errInvalidComplianceExpiry
	}
	return nil
}

// RequireTripRefs rejects a trip with no vehicle or no driver.
//
// Both columns are NOT NULL and have been since 0001. They accepted "" for as
// long as they were TEXT, so the API quietly stored a trip belonging to nobody.
// 0043 retyped them to uuid, where "" is not a value — the store now sends NULL
// and Postgres raises a null-violation the caller sees as a 502 naming a column
// rather than a field.
//
// Validating here says which field is missing, and keeps the stricter rule the
// uuid columns already enforce rather than working around it.
func RequireTripRefs(_ *gin.Context, t *models.Trip) error {
	if strings.TrimSpace(t.VehicleID) == "" {
		return fmt.Errorf("%w: vehicleId — a trip has to belong to a vehicle", errTripRefsRequired)
	}
	if strings.TrimSpace(t.DriverID) == "" {
		return fmt.Errorf("%w: driverId — a trip has to have a driver", errTripRefsRequired)
	}
	return nil
}

// RequireTripRefsOnUpdate applies the same rule to a PATCH, which merges onto
// the stored row: clearing either reference is the same null-violation.
func RequireTripRefsOnUpdate(c *gin.Context, t *models.Trip) error {
	return RequireTripRefs(c, t)
}

// ─── Driver–vehicle authorisation matrix (FR-DRV-04) ───────────────────────

// RequireAuthorisationRefs rejects a matrix row missing either side of the pair.
// Both columns are NOT NULL uuids, so without this the caller gets a raw
// null-violation as a 502 instead of being told which field they left out.
func RequireAuthorisationRefs(_ *gin.Context, a *models.PermitAuthorisation) error {
	if strings.TrimSpace(a.PermitClassID) == "" {
		return fmt.Errorf("%w: permitClassId — an authorisation needs a licence class", errAuthorisationRefsRequired)
	}
	if strings.TrimSpace(a.CategoryID) == "" {
		return fmt.Errorf("%w: categoryId — an authorisation needs a vehicle category", errAuthorisationRefsRequired)
	}
	return nil
}

// validateDriverVehicleAuthorisation refuses a pairing the operator's matrix
// contradicts.
//
// ── Why this is so careful about staying quiet ─────────────────────────────
// The matrix is configuration, and configuration arrives half-finished. Every
// existing deployment has an empty one, and drivers.permit_class is free text
// that many rows leave blank. A rule that denied on missing data would ground
// the fleet the day it shipped — which is a failure this service has already
// had once, when 20 seeded drivers all carried the placeholder permit expiry
// 2000-01-01, every one of them became undispatchable, and nothing on any screen
// said so.
//
// So it denies only on a positive, complete contradiction: the matrix is
// populated, the vehicle is classified, the driver's licence class resolves to a
// class the operator has defined, somebody has already said something about this
// category — and the pair is still absent. Anything less reads as "no opinion
// yet" and is allowed.
//
// A retired (inactive) class or category counts as unconfigured for the same
// reason: retiring one should switch its opinions off, not start refusing on it.
func validateDriverVehicleAuthorisation(
	ctx context.Context, repo *store.Repository, driverID, vehicleID string,
) error {
	if strings.TrimSpace(driverID) == "" || strings.TrimSpace(vehicleID) == "" {
		return nil
	}
	veh, err := repo.Vehicles.Get(ctx, vehicleID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errVehicleNotFound
		}
		return err
	}
	return validateDriverVehicleAuthorisationFor(ctx, repo, driverID, veh)
}

// validateDriverVehicleAuthorisationFor is the same rule against a vehicle
// already in hand.
//
// Two callers need it rather than the id form above. A vehicle being CREATED has
// no stored row to fetch, so the id form would answer "vehicle not found" and
// refuse the create outright. And a vehicle being UPDATED is being re-classified
// by the very request under validation — the merged item carries the categoryId
// the caller is setting, and the stored row still carries the old one, so
// re-fetching would check the rule against the value being replaced.
func validateDriverVehicleAuthorisationFor(
	ctx context.Context, repo *store.Repository, driverID string, veh models.Vehicle,
) error {
	if strings.TrimSpace(driverID) == "" {
		return nil
	}
	if strings.TrimSpace(veh.CategoryID) == "" {
		return nil // Gate 1: unclassified vehicle.
	}

	auths, err := repo.PermitAuthorisations.List(ctx)
	if err != nil {
		return err
	}
	if len(auths) == 0 {
		return nil // Gate 2: nobody has configured a matrix.
	}

	categories, err := repo.VehicleCategories.List(ctx)
	if err != nil {
		return err
	}
	category, ok := activeCategoryByID(categories, veh.CategoryID)
	if !ok {
		return nil // Gate 3: the category was retired or deleted.
	}

	drv, err := repo.Drivers.Get(ctx, driverID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errDriverNotFound
		}
		return err
	}
	classes, err := repo.PermitClasses.List(ctx)
	if err != nil {
		return err
	}
	class, ok := activeClassByCode(classes, drv.PermitClass)
	if !ok {
		return nil // Gate 4: the driver's licence class is not one the operator defined.
	}

	categoryMentioned := false
	for _, a := range auths {
		if a.CategoryID != category.ID {
			continue
		}
		categoryMentioned = true
		if a.PermitClassID == class.ID {
			return nil // Authorised.
		}
	}
	if !categoryMentioned {
		return nil // Gate 5: nothing has been said about this category yet.
	}

	return fmt.Errorf("%w: a %s licence is not authorised for %s vehicles",
		errPermitNotAuthorised, class.Code, category.Name)
}

func activeCategoryByID(all []models.VehicleCategory, id string) (models.VehicleCategory, bool) {
	for _, c := range all {
		if c.ID == id && c.Active {
			return c, true
		}
	}
	return models.VehicleCategory{}, false
}

// activeClassByCode matches drivers.permit_class, which is free text, against a
// configured class. Case- and space-insensitive: "ce" and "CE " are the same
// licence, and an operator typing either must not silently disable the rule.
func activeClassByCode(all []models.PermitClass, permitClass string) (models.PermitClass, bool) {
	want := strings.TrimSpace(permitClass)
	if want == "" {
		return models.PermitClass{}, false
	}
	for _, c := range all {
		if c.Active && strings.EqualFold(strings.TrimSpace(c.Code), want) {
			return c, true
		}
	}
	return models.PermitClass{}, false
}
