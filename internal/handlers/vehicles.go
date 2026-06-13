package handlers

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// NewVehicleResource returns a vehicle CRUD resource with registry validation and domain events.
func NewVehicleResource(repo *store.Repository, bus *events.Bus) *Resource[models.Vehicle, *models.Vehicle] {
	r := &Resource[models.Vehicle, *models.Vehicle]{
		Repo:       repo,
		Collection: repo.Vehicles,
		Entity:     "vehicle",
		IDPrefix:   "VEH",
		Events:     bus,
	}
	r.BeforeCreate = func(c *gin.Context, item *models.Vehicle) error {
		return validateVehicleDriver(c.Request.Context(), repo, item)
	}
	r.BeforeUpdate = func(c *gin.Context, item *models.Vehicle) error {
		return validateVehicleDriver(c.Request.Context(), repo, item)
	}
	r.AfterCreate = func(ctx context.Context, item models.Vehicle) {
		emitVehicleEvent(ctx, bus, events.TypeVehicleCreated, item, "")
	}
	r.AfterUpdate = func(ctx context.Context, before, after models.Vehicle) {
		emitVehicleEvent(ctx, bus, events.TypeVehicleUpdated, after, before.Status)
		if before.Status != after.Status && after.Status != "" {
			emitVehicleEvent(ctx, bus, events.TypeVehicleStatusChanged, after, before.Status)
		}
	}
	r.AfterDelete = func(ctx context.Context, id string) {
		if bus == nil || !bus.Enabled() {
			return
		}
		bus.PublishFleet(ctx, events.TypeVehicleDeleted, events.FleetEventData(map[string]string{
			"vehicleId": id,
		}), id, "")
	}
	return r
}

func validateVehicleDriver(ctx context.Context, repo *store.Repository, v *models.Vehicle) error {
	if v == nil || v.DriverID == "" {
		return nil
	}
	if err := validateDriverDispatch(ctx, repo, v.DriverID); err != nil {
		return err
	}
	// One driver per vehicle: the driver must not already be the assigned driver
	// of a different vehicle.
	return validateDriverNotOnAnotherVehicle(ctx, repo, v.DriverID, v.ID)
}

func emitVehicleEvent(ctx context.Context, bus *events.Bus, eventType string, v models.Vehicle, previousStatus string) {
	if bus == nil || !bus.Enabled() {
		return
	}
	data := events.FleetEventData(map[string]string{
		"vehicleId": v.ID,
		"plate":     v.Plate,
		"status":    v.Status,
	})
	if previousStatus != "" {
		data["previousStatus"] = previousStatus
	}
	if v.DriverID != "" {
		data["driverId"] = v.DriverID
	}
	if v.Vin != "" {
		data["vin"] = v.Vin
	}
	bus.PublishFleet(ctx, eventType, data, v.ID, "")
}
