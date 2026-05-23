package jobs

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FuelLinkMatchWindow is how close in time a manual fuel record and telemetry
// refuel event must be to auto-link.
const FuelLinkMatchWindow = 48 * time.Hour

// FuelLinkLitresTolerance is the relative litres difference allowed when matching.
const FuelLinkLitresTolerance = 0.20

// LinkFuelEvents matches unlinked refuel telemetry events to manual fuel_records
// for the same vehicle when volume and date align.
func LinkFuelEvents(ctx context.Context, pool *pgxpool.Pool, lookbackDays int) (linked int, err error) {
	if lookbackDays <= 0 {
		lookbackDays = 90
	}
	since := time.Now().UTC().AddDate(0, 0, -lookbackDays)

	const eventsQ = `
        SELECT e.id, e.vehicle_id, e.ts, e.delta_litres, e.delta_pct, e.odo,
               v.tank_capacity_litres
        FROM fuel_events e
        JOIN vehicles v ON v.id = e.vehicle_id
        WHERE e.kind = 'refuel'
          AND e.fuel_record_id IS NULL
          AND e.ts >= $1
        ORDER BY e.ts ASC`

	rows, err := pool.Query(ctx, eventsQ, since)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type candidate struct {
		id       int64
		vehicle  string
		ts       time.Time
		litres   float64
		hasLitres bool
		odo      *float64
	}

	var events []candidate
	for rows.Next() {
		var c candidate
		var deltaLitres *float64
		var deltaPct float64
		var tankCap *int
		if err := rows.Scan(&c.id, &c.vehicle, &c.ts, &deltaLitres, &deltaPct, &c.odo, &tankCap); err != nil {
			return linked, err
		}
		if deltaLitres != nil && *deltaLitres > 0 {
			c.litres = *deltaLitres
			c.hasLitres = true
		} else if tankCap != nil && *tankCap > 0 && deltaPct > 0 {
			c.litres = float64(*tankCap) * deltaPct / 100.0
			c.hasLitres = true
		}
		events = append(events, c)
	}
	if err := rows.Err(); err != nil {
		return linked, err
	}

	const recordsQ = `
        SELECT id, vehicle_id, date::timestamptz, litres, odo, fuel_event_id
        FROM fuel_records
        WHERE vehicle_id = $1
          AND fuel_event_id IS NULL
          AND date >= ($2::date - 3)
          AND date <= ($2::date + 3)`

	const linkQ = `
        UPDATE fuel_events SET fuel_record_id = $2 WHERE id = $1 AND fuel_record_id IS NULL`
	const linkRecQ = `
        UPDATE fuel_records SET fuel_event_id = $2 WHERE id = $1 AND fuel_event_id IS NULL`

	for _, ev := range events {
		recRows, err := pool.Query(ctx, recordsQ, ev.vehicle, ev.ts)
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
		tx, err := pool.Begin(ctx)
		if err != nil {
			return linked, err
		}
		tag, err := tx.Exec(ctx, linkQ, ev.id, bestID)
		if err != nil {
			_ = tx.Rollback(ctx)
			return linked, err
		}
		if tag.RowsAffected() == 0 {
			_ = tx.Rollback(ctx)
			continue
		}
		if _, err := tx.Exec(ctx, linkRecQ, bestID, ev.id); err != nil {
			_ = tx.Rollback(ctx)
			return linked, err
		}
		if err := tx.Commit(ctx); err != nil {
			return linked, err
		}
		linked++
	}
	return linked, nil
}

// LinkFuelEventsSummary returns human-readable stats for logging.
func LinkFuelEventsSummary(linked int, err error) string {
	if err != nil {
		return fmt.Sprintf("fuel link failed: %v", err)
	}
	return fmt.Sprintf("fuel link: %d refuel events matched to fuel_records", linked)
}
