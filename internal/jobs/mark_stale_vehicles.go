package jobs

import (
	"context"
	"time"

	"github.com/iag/fleet-iot/iot"
)

// MarkStaleVehiclesOffline sets moving/idle vehicles to offline when last_seen is older than iot.StaleVehicleAfter().
func MarkStaleVehiclesOffline(ctx context.Context, store *iot.Store) (int64, error) {
	cutoff := time.Now().UTC().Add(-iot.StaleVehicleAfter())
	return store.MarkStaleVehiclesOffline(ctx, cutoff)
}
