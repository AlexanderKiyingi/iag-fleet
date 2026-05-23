package jobs

import (
	"context"
	"time"

	"github.com/iag/fleet-iot/iot"
)

// PurgeTelemetryPings deletes raw rows in telemetry_timeseries older than the
// retention window (cutoff = now - days).
func PurgeTelemetryPings(ctx context.Context, store *iot.Store, days int) (int64, error) {
	if days < 1 {
		days = 1
	}
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	return store.PurgeBefore(ctx, cutoff)
}
