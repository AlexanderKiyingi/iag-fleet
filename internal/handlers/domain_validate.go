package handlers

import (
	"context"
	"errors"
	"fmt"
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
	if !store.DriverPermitOK(drv, time.Now().UTC()) {
		return errDriverPermitInvalid
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
