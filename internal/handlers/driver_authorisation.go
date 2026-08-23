package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Driver-vehicle authorisation matrix — PRD FR-DRV-04.
//
// A driver may only be assigned to vehicle categories their permit class
// authorises. The taxonomy is not compiled in: categories, permit classes and
// the mapping between them are records an operator maintains, because which
// class may operate a grader is a licensing question whose answer differs by
// country and is not ours to guess.
//
// ── Why this fails open ─────────────────────────────────────────────────────
// Three separate "not configured" states are treated as "no opinion", not as
// "denied":
//
//   - the matrix is empty                  nothing has been configured yet
//   - the vehicle has no category          it has not been classified
//   - the driver has no permit class       their record is incomplete
//
// Closing any of them would ground the fleet the moment this deployed. That is
// not hypothetical: every one of the 20 drivers in this fleet is already
// un-dispatchable because a seed migration left permit_expiry at 2000-01-01,
// and nothing surfaced it until somebody tried to assign one. A second
// fleet-wide block introduced by an empty configuration table would be the same
// mistake with a different cause.
//
// The rule binds only where an operator has actually expressed an intent: this
// class, this category, authorised or not.

// driverAuthorisedForVehicle reports whether the driver's permit class covers
// the vehicle's category. A nil error means "allowed, or nothing to say".
func driverAuthorisedForVehicle(
	ctx context.Context,
	repo *store.Repository,
	driverID string,
	vehicleID string,
) error {
	if repo == nil || driverID == "" || vehicleID == "" {
		return nil
	}

	auths, err := repo.PermitAuths.List(ctx)
	if err != nil {
		return err
	}
	// Nothing configured: the matrix expresses no opinion about anybody.
	if len(auths) == 0 {
		return nil
	}

	vehicle, err := repo.Vehicles.Get(ctx, vehicleID)
	if err != nil {
		// Existence is somebody else's check; not being able to read the
		// vehicle here must not turn into an authorisation failure.
		return nil
	}
	if strings.TrimSpace(vehicle.CategoryID) == "" {
		// Unclassified vehicle. The operator has not said what it is, so the
		// matrix cannot say who may drive it.
		return nil
	}

	driver, err := repo.Drivers.Get(ctx, driverID)
	if err != nil {
		return nil
	}
	held := driverPermitClasses(driver)
	if len(held) == 0 {
		// Incomplete driver record rather than an unauthorised one. Blocking
		// here would ground every driver whose permit class was never entered
		// — which today is all of them.
		return nil
	}

	classes, err := repo.PermitClasses.List(ctx)
	if err != nil {
		return err
	}
	// Match on the code as written, case-insensitively: an operator typing "b"
	// on a driver record and "B" on the class list means the same thing.
	classIDsByCode := make(map[string]string, len(classes))
	codeByID := make(map[string]string, len(classes))
	for _, c := range classes {
		classIDsByCode[strings.ToLower(strings.TrimSpace(c.Code))] = c.ID
		codeByID[c.ID] = c.Code
	}

	allowed := make(map[string]struct{})
	for _, a := range auths {
		allowed[a.PermitClassID+">"+a.CategoryID] = struct{}{}
	}

	var recognised []string
	for _, code := range held {
		id, ok := classIDsByCode[strings.ToLower(code)]
		if !ok {
			// A class the operator has not defined. Unknown, not denied — the
			// same reasoning as an unclassified vehicle.
			continue
		}
		recognised = append(recognised, code)
		if _, ok := allowed[id+">"+vehicle.CategoryID]; ok {
			return nil
		}
	}
	if len(recognised) == 0 {
		return nil
	}

	categoryName := vehicle.CategoryID
	if cat, cerr := repo.VehicleCategories.Get(ctx, vehicle.CategoryID); cerr == nil && cat.Name != "" {
		categoryName = cat.Name
	}
	return fmt.Errorf(
		"%w: permit class %s is not authorised for %s",
		errDriverEligibility,
		strings.Join(recognised, "/"),
		categoryName,
	)
}

// driverPermitClasses splits the driver's recorded class. PRD FR-DRV-02 says
// "licence class(es)" — a driver commonly holds more than one — and the model
// carries a single string, so a separated list is accepted and any one of them
// authorising the category is enough.
func driverPermitClasses(d models.Driver) []string {
	raw := strings.TrimSpace(d.PermitClass)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '/' || r == '|' || r == ' '
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
