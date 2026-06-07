package jobs

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// FuelLinkMatchWindow is how close in time a manual fuel record and telemetry
// refuel event must be to auto-link.
const FuelLinkMatchWindow = 48 * time.Hour

// FuelLinkLitresTolerance is the relative litres difference allowed when matching.
const FuelLinkLitresTolerance = 0.20

// LinkFuelEvents matches unlinked refuel telemetry events to manual fuel_records
// for the same vehicle when volume and date align. fuel_events live on the
// telemetry pool when split; fuel_records and vehicles stay operational.
func LinkFuelEvents(ctx context.Context, db store.FuelDB, lookbackDays int, vehicleID string) (linked int, err error) {
	if lookbackDays <= 0 {
		lookbackDays = 90
	}
	since := time.Now().UTC().AddDate(0, 0, -lookbackDays)
	op := db.Operational
	evPool := db.Events()

	const eventsQ = `
        SELECT id, vehicle_id, ts, delta_litres, delta_pct, odo
        FROM fuel_events
        WHERE kind = 'refuel'
          AND fuel_record_id IS NULL
          AND ts >= $1
          AND ($2 = '' OR vehicle_id = $2)
        ORDER BY ts ASC`

	rows, err := evPool.Query(ctx, eventsQ, since, vehicleID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type candidate struct {
		id        int64
		vehicle   string
		ts        time.Time
		deltaPct  float64
		litres    float64
		hasLitres bool
		odo       *float64
	}

	var events []candidate
	vehIDs := make(map[string]struct{})
	for rows.Next() {
		var c candidate
		var deltaLitres *float64
		if err := rows.Scan(&c.id, &c.vehicle, &c.ts, &deltaLitres, &c.deltaPct, &c.odo); err != nil {
			return linked, err
		}
		vehIDs[c.vehicle] = struct{}{}
		if deltaLitres != nil && *deltaLitres > 0 {
			c.litres = *deltaLitres
			c.hasLitres = true
		}
		events = append(events, c)
	}
	if err := rows.Err(); err != nil {
		return linked, err
	}

	tankByVeh, err := loadTankCapacities(ctx, op, vehIDs)
	if err != nil {
		return linked, err
	}
	for i := range events {
		c := &events[i]
		if c.hasLitres {
			continue
		}
		if cap := tankByVeh[c.vehicle]; cap > 0 && c.deltaPct > 0 {
			c.litres = float64(cap) * c.deltaPct / 100.0
			c.hasLitres = true
		}
	}

	const recordsQ = `
        SELECT id, vehicle_id, date::timestamptz, litres, odo, fuel_event_id
        FROM fuel_records
        WHERE vehicle_id = $1
          AND fuel_event_id IS NULL
          AND date >= ($2::date - 3)
          AND date <= ($2::date + 3)`

	const linkEventQ = `UPDATE fuel_events SET fuel_record_id = $2 WHERE id = $1 AND fuel_record_id IS NULL`
	const linkRecQ = `UPDATE fuel_records SET fuel_event_id = $2 WHERE id = $1 AND fuel_event_id IS NULL`

	for _, ev := range events {
		recRows, err := op.Query(ctx, recordsQ, ev.vehicle, ev.ts)
		if err != nil {
			return linked, err
		}
		var bestID string
		var bestScore float64 = -1
		for recRows.Next() {
			var recID, veh string
			var recDate time.Time
			var litres, odo float64
			var existingEv *int64
			if err := recRows.Scan(&recID, &veh, &recDate, &litres, &odo, &existingEv); err != nil {
				recRows.Close()
				return linked, err
			}
			if existingEv != nil {
				continue
			}
			timeDiff := ev.ts.Sub(recDate)
			if timeDiff < 0 {
				timeDiff = -timeDiff
			}
			if timeDiff > FuelLinkMatchWindow {
				continue
			}
			if ev.hasLitres && litres > 0 {
				rel := math.Abs(litres-ev.litres) / litres
				if rel > FuelLinkLitresTolerance {
					continue
				}
			}
			score := 1.0 / (1.0 + timeDiff.Hours())
			if ev.odo != nil && odo > 0 {
				odoDiff := math.Abs(odo - *ev.odo)
				score += 1.0 / (1.0 + odoDiff/10.0)
			}
			if score > bestScore {
				bestScore = score
				bestID = recID
			}
		}
		recRows.Close()
		if bestID == "" {
			continue
		}
		tag, err := evPool.Exec(ctx, linkEventQ, ev.id, bestID)
		if err != nil {
			return linked, err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if _, err := op.Exec(ctx, linkRecQ, bestID, ev.id); err != nil {
			return linked, fmt.Errorf("link fuel_record %s to event %d: %w", bestID, ev.id, err)
		}
		linked++
	}
	return linked, nil
}

func loadTankCapacities(ctx context.Context, pool *pgxpool.Pool, vehIDs map[string]struct{}) (map[string]int, error) {
	out := make(map[string]int, len(vehIDs))
	if len(vehIDs) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(vehIDs))
	for id := range vehIDs {
		ids = append(ids, id)
	}
	rows, err := pool.Query(ctx, `SELECT id, tank_capacity_litres FROM vehicles WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var cap *int
		if err := rows.Scan(&id, &cap); err != nil {
			return nil, err
		}
		if cap != nil && *cap > 0 {
			out[id] = *cap
		}
	}
	return out, rows.Err()
}

// LinkFuelEventsSummary returns human-readable stats for logging.
func LinkFuelEventsSummary(linked int, err error) string {
	if err != nil {
		return fmt.Sprintf("fuel link failed: %v", err)
	}
	return fmt.Sprintf("fuel link: %d refuel events matched to fuel_records", linked)
}
