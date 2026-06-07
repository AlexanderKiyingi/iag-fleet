package jobs

import (
	"context"

	"github.com/iag/fleet-tool/backend/internal/store"
)

// DefaultPMWithinDays matches GET /api/pm-schedules/due lookahead.
const DefaultPMWithinDays = 14

// DefaultPMWithinKm matches GET /api/pm-schedules/due odometer lookahead.
const DefaultPMWithinKm = 500.0

// EvaluatePM creates work orders for due PM schedules.
func EvaluatePM(ctx context.Context, repo *store.Repository, withinDays int, withinKm float64) (store.PMEvaluateResult, error) {
	if withinDays < 0 {
		withinDays = DefaultPMWithinDays
	}
	if withinKm < 0 {
		withinKm = DefaultPMWithinKm
	}
	return repo.EvaluatePMSchedules(ctx, withinDays, withinKm)
}
