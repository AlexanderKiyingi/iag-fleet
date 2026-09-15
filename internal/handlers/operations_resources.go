package handlers

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// The operations records (migration 0057) are generic CRUD resources. What
// makes them more than a table is a derived column or a validation rule each,
// applied in the create/update hooks so the /bulk verbs get it too. Derived
// columns are recomputed on every write, so a client echoing back what it
// read cannot make them wrong — which is why they are not ServerOwnedFields:
// a 409 there would punish the PUT-shaped clients for telling the truth.

// NewWeighbridgeResource derives `overweight` from the weights and limits.
func NewWeighbridgeResource(repo *store.Repository) *Resource[models.WeighbridgeTicket, *models.WeighbridgeTicket] {
	r := &Resource[models.WeighbridgeTicket, *models.WeighbridgeTicket]{
		Repo: repo, Collection: repo.WeighbridgeTickets,
		Entity: "weighbridge_ticket", IDPrefix: "WBT",
	}
	hook := func(_ *gin.Context, t *models.WeighbridgeTicket) error {
		if t.GrossWeightKg <= 0 {
			return fmt.Errorf("grossWeightKg must be greater than zero")
		}
		if strings.TrimSpace(t.Site) == "" {
			return fmt.Errorf("site is required")
		}
		t.Overweight = (t.GrossLimitKg != nil && *t.GrossLimitKg > 0 && t.GrossWeightKg > *t.GrossLimitKg) ||
			(t.AxleLimitKg != nil && t.AxleWeightKg != nil && *t.AxleLimitKg > 0 && *t.AxleWeightKg > *t.AxleLimitKg)
		if t.Status == "" {
			t.Status = "ok"
		}
		// A ticket that is over a limit cannot say it is fine; "cleared" is a
		// deliberate later state and is left alone.
		if t.Overweight && t.Status == "ok" {
			t.Status = "overweight"
		}
		return nil
	}
	r.BeforeCreate, r.BeforeUpdate = hook, hook
	return r
}

// NewFuelCardReconciliationResource derives `discrepancy` = statement − system.
func NewFuelCardReconciliationResource(repo *store.Repository) *Resource[models.FuelCardReconciliation, *models.FuelCardReconciliation] {
	r := &Resource[models.FuelCardReconciliation, *models.FuelCardReconciliation]{
		Repo: repo, Collection: repo.FuelCardReconciliations,
		Entity: "fuel_card_reconciliation", IDPrefix: "FCR",
	}
	hook := func(_ *gin.Context, f *models.FuelCardReconciliation) error {
		if strings.TrimSpace(f.Account) == "" {
			return fmt.Errorf("account is required")
		}
		f.Discrepancy = math.Round((f.StatementBalance-f.SystemBalance)*100) / 100
		if f.Status == "" {
			f.Status = "open"
		}
		return nil
	}
	r.BeforeCreate, r.BeforeUpdate = hook, hook
	return r
}

// NewEmissionsResource derives CO₂e from litres, and per tonne-km when both
// km and tonnes are present. The factor is models.DieselKgCO2ePerLitre.
func NewEmissionsResource(repo *store.Repository) *Resource[models.EmissionsEntry, *models.EmissionsEntry] {
	r := &Resource[models.EmissionsEntry, *models.EmissionsEntry]{
		Repo: repo, Collection: repo.EmissionsEntries,
		Entity: "emissions_entry", IDPrefix: "EMS",
	}
	hook := func(_ *gin.Context, e *models.EmissionsEntry) error {
		if e.Litres <= 0 {
			return fmt.Errorf("litres must be greater than zero")
		}
		e.CO2eKg = math.Round(e.Litres*models.DieselKgCO2ePerLitre*100) / 100
		e.CO2ePerTonneKm = nil
		if e.Km != nil && e.Tonnes != nil && *e.Km > 0 && *e.Tonnes > 0 {
			v := math.Round(e.CO2eKg/(*e.Km**e.Tonnes)*10000) / 10000
			e.CO2ePerTonneKm = &v
		}
		if e.Status == "" {
			e.Status = "draft"
		}
		return nil
	}
	r.BeforeCreate, r.BeforeUpdate = hook, hook
	return r
}

// newPlainResource is the no-hook case with a default status.
func newPlainResource[T any, PT store.IdentifiablePtr[T]](repo *store.Repository, coll *store.Collection[T, PT], entity, prefix string, defaults func(*T)) *Resource[T, PT] {
	r := &Resource[T, PT]{Repo: repo, Collection: coll, Entity: entity, IDPrefix: prefix}
	if defaults != nil {
		hook := func(_ *gin.Context, item *T) error { defaults(item); return nil }
		r.BeforeCreate, r.BeforeUpdate = hook, hook
	}
	return r
}

// RegisterOperationsResources mounts the nine 0057 resources.
func RegisterOperationsResources(api *gin.RouterGroup, repo *store.Repository) {
	NewWeighbridgeResource(repo).Register(api, "/weighbridge-tickets")
	NewFuelCardReconciliationResource(repo).Register(api, "/fuel-card-reconciliations")
	NewEmissionsResource(repo).Register(api, "/emissions")

	newPlainResource(repo, repo.VehicleDiagnostics, "vehicle_diagnostic", "DTC",
		func(d *models.VehicleDiagnostic) {
			if d.Status == "" {
				d.Status = "open"
			}
			if d.Severity == "" {
				d.Severity = "info"
			}
		}).Register(api, "/diagnostics")

	newPlainResource(repo, repo.DriverHOSLogs, "driver_hos_log", "HOS",
		func(h *models.DriverHOSLog) {
			if h.Status == "" {
				h.Status = "ok"
			}
		}).Register(api, "/hos-logs")

	newPlainResource(repo, repo.DriverSafetyScores, "driver_safety_score", "DSS",
		func(s *models.DriverSafetyScore) {
			if s.Status == "" {
				s.Status = "open"
			}
		}).Register(api, "/driver-safety-scores")

	newPlainResource(repo, repo.ServiceReminders, "service_reminder", "SRM",
		func(s *models.ServiceReminder) {
			if s.Status == "" {
				s.Status = "open"
			}
			if s.Source == "" {
				s.Source = "pm-schedule"
			}
		}).Register(api, "/service-reminders")

	newPlainResource(repo, repo.RouteETAs, "route_eta", "ETA",
		func(r *models.RouteETA) {
			if r.Status == "" {
				r.Status = "planned"
			}
		}).Register(api, "/route-etas")

	NewTripPODResource(repo).Register(api, "/trip-pods")

	newPlainResource(repo, repo.Carriers, "carrier", "CAR",
		func(c *models.Carrier) {
			if c.Status == "" {
				c.Status = "active"
			}
		}).Register(api, "/carriers")
}

// NewTripPODResource is proof of delivery (0058). A POD needs a trip and a
// receiver; recording one is what moves the trip to completed, so the
// transition happens here rather than being a separate PATCH the client
// might forget.
func NewTripPODResource(repo *store.Repository) *Resource[models.TripPOD, *models.TripPOD] {
	r := &Resource[models.TripPOD, *models.TripPOD]{
		Repo: repo, Collection: repo.TripPODs,
		Entity: "trip_pod", IDPrefix: "POD",
	}
	hook := func(c *gin.Context, p *models.TripPOD) error {
		if strings.TrimSpace(p.TripID) == "" {
			return fmt.Errorf("tripId is required — a proof of delivery belongs to a trip")
		}
		if strings.TrimSpace(p.ReceivedBy) == "" {
			return fmt.Errorf("receivedBy is required")
		}
		if _, err := repo.Trips.Get(c.Request.Context(), p.TripID); err != nil {
			return fmt.Errorf("trip %s not found", p.TripID)
		}
		if p.Condition == "" {
			p.Condition = "good"
		}
		if p.Status == "" {
			p.Status = "delivered"
		}
		return nil
	}
	r.BeforeCreate, r.BeforeUpdate = hook, hook
	r.AfterCreate = func(ctx context.Context, p models.TripPOD) {
		_, _ = repo.Trips.Update(ctx, p.TripID, func(t *models.Trip) {
			if t.Status != "cancelled" {
				t.Status = "completed"
			}
		})
	}
	return r
}
