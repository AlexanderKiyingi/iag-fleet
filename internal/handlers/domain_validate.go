package handlers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

var (
	errDriverPermitInvalid   = errors.New("driver permit expired or missing")
	errInvalidPMSchedule     = errors.New("invalid PM schedule")
	errInvalidMaintenanceStatus = errors.New("invalid maintenance status")
	errInvalidComplianceDoc  = errors.New("invalid compliance document")
	errInvalidComplianceExpiry = errors.New("expiry must be today or in the future")
	errDriverDoubleBooked     = errors.New("driver already has an overlapping journey in this period")
	errVehicleDoubleBooked    = errors.New("vehicle already has an overlapping journey in this period")
	errDriverAlreadyOnVehicle = errors.New("driver is already assigned to another vehicle")
	errDriverEligibility      = errors.New("driver not eligible for dispatch")
	errVehicleNotDispatchable = errors.New("vehicle not dispatchable")
	errInvalidFuelRecord      = errors.New("invalid fuel record")
)

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
