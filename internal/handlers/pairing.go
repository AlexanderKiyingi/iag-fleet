package handlers

import (
	"context"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// syncVehicleDriverPairing keeps driver.vehicleId in step with vehicle.driverId
// after a vehicle is created/updated: the assigned driver points back at this
// vehicle, and any other driver still pointing here is detached. Best-effort —
// the vehicle is already persisted, and store-level updates don't re-invoke the
// driver's handler hooks, so there is no reciprocal-sync loop.
func syncVehicleDriverPairing(ctx context.Context, repo *store.Repository, v models.Vehicle) {
	drivers, err := repo.Drivers.List(ctx)
	if err != nil {
		return
	}
	for _, d := range drivers {
		switch {
		case v.DriverID != "" && d.ID == v.DriverID:
			if d.VehicleID != v.ID {
				_, _ = repo.Drivers.Update(ctx, d.ID, func(dd *models.Driver) { dd.VehicleID = v.ID })
			}
		case d.VehicleID == v.ID:
			_, _ = repo.Drivers.Update(ctx, d.ID, func(dd *models.Driver) { dd.VehicleID = "" })
		}
	}
}

// syncDriverVehiclePairing is the mirror: after a driver is created/updated,
// keep vehicle.driverId in step with driver.vehicleId.
func syncDriverVehiclePairing(ctx context.Context, repo *store.Repository, d models.Driver) {
	vehicles, err := repo.Vehicles.List(ctx)
	if err != nil {
		return
	}
	for _, v := range vehicles {
		switch {
		case d.VehicleID != "" && v.ID == d.VehicleID:
			if v.DriverID != d.ID {
				_, _ = repo.Vehicles.Update(ctx, v.ID, func(vv *models.Vehicle) { vv.DriverID = d.ID })
			}
		case v.DriverID == d.ID:
			_, _ = repo.Vehicles.Update(ctx, v.ID, func(vv *models.Vehicle) { vv.DriverID = "" })
		}
	}
}
