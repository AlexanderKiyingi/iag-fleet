// Package jobs contains batch maintenance tasks shared by CLI entrypoints
// (telemetry-aggregate, telemetry-purge, fleet-jobs).
package jobs

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-iot/iot"
)

const DayLayout = "2006-01-02"

// VehicleDayPair is one (vehicle, UTC day) rollup unit.
type VehicleDayPair struct {
	VehicleID string
	Day       time.Time
}

// AggregateTelemetry rolls raw pings into telemetry_daily and fuel_events
// for every (vehicle, day) in range. to is exclusive (half-open [from, to)).
func AggregateTelemetry(ctx context.Context, store *iot.Store, bus *events.Bus, pool *pgxpool.Pool, from, to time.Time, vehicle string) (written, eventsWritten, failed int, err error) {
	pairs, err := loadPairs(ctx, store, vehicle, from, to)
	if err != nil {
		return 0, 0, 0, err
	}
	if len(pairs) == 0 {
		return 0, 0, 0, nil
	}

	vehicleSet := make(map[string]struct{}, len(pairs))
	for _, p := range pairs {
		vehicleSet[p.VehicleID] = struct{}{}
	}
	vehicleIDs := make([]string, 0, len(vehicleSet))
	for v := range vehicleSet {
		vehicleIDs = append(vehicleIDs, v)
	}
	caps, err := store.VehicleTankCapacities(ctx, vehicleIDs)
	if err != nil {
		return 0, 0, 0, err
	}

	for _, p := range pairs {
		pings, err := store.PingsForDay(ctx, p.VehicleID, p.Day)
		if err != nil {
			log.Printf("  %s %s: load pings failed: %v", p.VehicleID, p.Day.Format(DayLayout), err)
			failed++
			continue
		}
		res := iot.AggregateDay(p.VehicleID, p.Day, pings, caps[p.VehicleID])
		if err := store.UpsertDaily(ctx, res.Summary); err != nil {
			log.Printf("  %s %s: upsert failed: %v", p.VehicleID, p.Day.Format(DayLayout), err)
			failed++
			continue
		}
		if n, err := store.InsertFuelEvents(ctx, res.FuelEvents); err != nil {
			log.Printf("  %s %s: insert fuel events failed: %v", p.VehicleID, p.Day.Format(DayLayout), err)
		} else {
			eventsWritten += n
			if n > 0 {
				publishTelemetryFuelEvents(ctx, bus, res.FuelEvents)
			}
		}
		written++
		fuelLine := ""
		if res.Summary.FuelUsedLitres != nil {
			fuelLine = ", fuel=" + fmt.Sprintf("%.2f", *res.Summary.FuelUsedLitres) + " L"
		}
		evLine := ""
		if len(res.FuelEvents) > 0 {
			evLine = ", events=" + fmt.Sprintf("%d", len(res.FuelEvents))
		}
		log.Printf("  %s %s: pings=%d distance=%.2f km moving=%dm idle=%dm%s%s",
			p.VehicleID, p.Day.Format(DayLayout),
			res.Summary.PingCount, res.Summary.DistanceKm, res.Summary.MovingMinutes, res.Summary.IdleMinutes,
			fuelLine, evLine)
	}
	if pool != nil {
		if linked, linkErr := LinkFuelEvents(ctx, pool, 90); linkErr != nil {
			log.Printf("fuel link after aggregate: %v", linkErr)
		} else if linked > 0 {
			log.Printf("fuel link after aggregate: %d matched", linked)
		}
	}
	return written, eventsWritten, failed, nil
}

// ResolveAggregateRange parses --from / --to style strings; empty from defaults
// to yesterday UTC. Returns half-open [from, to).
func ResolveAggregateRange(fromStr, toStr string) (from, to time.Time, err error) {
	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1).Truncate(24 * time.Hour)

	from = yesterday
	if fromStr != "" {
		t, err := time.ParseInLocation(DayLayout, fromStr, time.UTC)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--from: %w", err)
		}
		from = t
	}

	toEnd := from.Add(24 * time.Hour)
	if toStr != "" {
		t, err := time.ParseInLocation(DayLayout, toStr, time.UTC)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to: %w", err)
		}
		toEnd = t.Add(24 * time.Hour)
	}
	if !toEnd.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("--to must be on/after --from")
	}
	if toEnd.Sub(from) > 90*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("range capped at 90 days; run multiple invocations")
	}
	return from, toEnd, nil
}

func loadPairs(ctx context.Context, store *iot.Store, vehicle string, from, to time.Time) ([]VehicleDayPair, error) {
	if vehicle != "" {
		var out []VehicleDayPair
		for d := from; d.Before(to); d = d.Add(24 * time.Hour) {
			out = append(out, VehicleDayPair{VehicleID: vehicle, Day: d})
		}
		return out, nil
	}
	rows, err := store.DistinctVehicleDays(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]VehicleDayPair, 0, len(rows))
	for _, r := range rows {
		out = append(out, VehicleDayPair{VehicleID: r.VehicleID, Day: r.Day})
	}
	return out, nil
}
