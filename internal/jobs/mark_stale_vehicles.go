package jobs

import (
	"context"
	"time"

	"github.com/iag/fleet-iot/iot"
)

// MarkStaleVehiclesOffline sets moving/idle vehicles to offline when last_seen is older than iot.StaleVehicleAfter().
func MarkStaleVehiclesOffline(ctx context.Context, store *iot.Store) (int64, error) {
	cutoff := time.Now().UTC().Add(-iot.StaleVehicleAfter())
	changes, err := store.MarkStaleVehiclesOffline(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	_ = store.PublishStatusChanges(ctx, changes)
	return int64(len(changes)), nil
}
