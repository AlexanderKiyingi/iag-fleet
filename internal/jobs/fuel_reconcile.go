package jobs

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	fuelpkg "github.com/iag/fleet-tool/backend/internal/fuel"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

const telemetryDropMatchWindow = 7 * 24 * time.Hour

// ReconcileFuelResult summarises link + anomaly sweep outcomes.
type ReconcileFuelResult struct {
	Linked            int `json:"linked"`
	Reflagged         int `json:"reflagged"`
	TelemetryMissing  int `json:"telemetryMissing"`
	TelemetryMismatch int `json:"telemetryMismatch"`
	TelemetryDrops    int `json:"telemetryDrops"`
}

// ReconcileFuel links telemetry refuels, bridges tank-drop events, then re-runs
// enriched anomaly detection. When vehicleID is non-empty, only that vehicle is processed.
func ReconcileFuel(ctx context.Context, db store.FuelDB, lookbackDays int, vehicleID string) (ReconcileFuelResult, error) {
	var out ReconcileFuelResult
	if lookbackDays <= 0 {
		lookbackDays = 90
	}

	linked, err := LinkFuelEvents(ctx, db, lookbackDays, vehicleID)
	if err != nil {
		return out, err
	}
	out.Linked = linked

	repo := store.NewRepository(db.Operational)
	repo.AttachTelemetry(db.Telemetry)

	since := time.Now().UTC().AddDate(0, 0, -lookbackDays).Format("2006-01-02")

	allFuel, err := repo.Fuel.List(ctx)
	if err != nil {
		return out, err
	}

	vehicles, err := repo.Vehicles.List(ctx)
	if err != nil {
		return out, err
	}
	tankByVeh := make(map[string]int, len(vehicles))
	trackerByVeh := make(map[string]bool, len(vehicles))
	for _, v := range vehicles {
		if vehicleID != "" && v.ID != vehicleID {
			continue
		}
		if v.TankCapacityLitres != nil {
			tankByVeh[v.ID] = *v.TankCapacityLitres
		}
		trackerByVeh[v.ID] = v.FuelTracker
	}

	dropHints, err := loadTelemetryDropHints(ctx, db, lookbackDays, vehicleID, tankByVeh)
	if err != nil {
		return out, err
	}

	byVehicle := make(map[string][]models.FuelRecord)
	for _, r := range allFuel {
		if r.Date < since {
			continue
		}
		if vehicleID != "" && r.VehicleID != vehicleID {
			continue
		}
		byVehicle[r.VehicleID] = append(byVehicle[r.VehicleID], r)
	}

	for vehID, rows := range byVehicle {
		tank := tankByVeh[vehID]
		tracked := trackerByVeh[vehID]
		for i := range rows {
			rec := rows[i]
			telL := 0.0
			if rec.FuelEventID != nil && *rec.FuelEventID > 0 {
				telL = fuelpkg.LoadTelemetryLitres(ctx, repo, *rec.FuelEventID)
			}
			actx := fuelpkg.BuildAnomalyContextFromHistory(&rec, allFuel, tank, tracked, telL)
			actx.CheckTelemetryMissing = tracked
			if hint, ok := nearestDropHint(dropHints[vehID], rec.Date); ok {
				actx.TelemetryDrop = hint
			}

			before := rec.Anomaly != nil && *rec.Anomaly
			beforeTypes := append(models.AnomalyTypes(nil), rec.AnomalyTypes...)
			fuelpkg.ApplyEnrichment(&rec, actx)
			after := rec.Anomaly != nil && *rec.Anomaly

			if !after && !before {
				continue
			}
			if after == before && anomalyTypesEqual(rec.AnomalyTypes, beforeTypes) {
				continue
			}

			_, err := repo.Fuel.Update(ctx, rec.ID, func(fr *models.FuelRecord) {
				*fr = rec
				if after && !before {
					if len(fr.AnomalyHistory) == 0 {
						fr.AnomalyHistory = append(fr.AnomalyHistory, models.AnomalyHistoryEvent{
							At:    time.Now().UTC().Format(time.RFC3339),
							Event: "flagged",
							Note:  fr.AnomalyReason,
						})
					}
					fr.AnomalyStatus = "open"
				}
				if actx.TelemetryDrop != nil && containsType(fr.AnomalyTypes, "telemetry-drop") {
					marker := fmt.Sprintf("fuel_event:%d", actx.TelemetryDrop.EventID)
					if !historyNoteContains(fr.AnomalyHistory, marker) {
						fr.AnomalyHistory = append(fr.AnomalyHistory, models.AnomalyHistoryEvent{
							At:    time.Now().UTC().Format(time.RFC3339),
							Event: "flagged",
							Note:  marker + " · " + fr.AnomalyReason,
						})
					}
				}
			})
			if err != nil {
				return out, fmt.Errorf("update fuel %s: %w", rec.ID, err)
			}
			out.Reflagged++
			for _, t := range rec.AnomalyTypes {
				switch t {
				case "telemetry-missing":
					out.TelemetryMissing++
				case "telemetry-mismatch":
					out.TelemetryMismatch++
				case "telemetry-drop":
					out.TelemetryDrops++
				}
			}
		}
	}
	return out, nil
}

func loadTelemetryDropHints(ctx context.Context, db store.FuelDB, lookbackDays int, vehicleID string, tankByVeh map[string]int) (map[string][]*fuelpkg.TelemetryDropHint, error) {
	since := time.Now().UTC().AddDate(0, 0, -lookbackDays)
	const q = `
        SELECT id, vehicle_id, ts, delta_litres, delta_pct, confidence
        FROM fuel_events
        WHERE kind = 'drop'
          AND ts >= $1
          AND confidence IN ('high', 'medium')
          AND ($2 = '' OR vehicle_id = $2)
        ORDER BY ts ASC`
	rows, err := db.Events().Query(ctx, q, since, vehicleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]*fuelpkg.TelemetryDropHint)
	for rows.Next() {
		var id int64
		var veh, conf string
		var ts time.Time
		var deltaLitres *float64
		var deltaPct float64
		if err := rows.Scan(&id, &veh, &ts, &deltaLitres, &deltaPct, &conf); err != nil {
			return nil, err
		}
		litres := 0.0
		if deltaLitres != nil {
			litres = math.Abs(*deltaLitres)
		} else if cap := tankByVeh[veh]; cap > 0 {
			litres = float64(cap) * math.Abs(deltaPct) / 100.0
		}
		out[veh] = append(out[veh], &fuelpkg.TelemetryDropHint{
			EventID:     id,
			TS:          ts,
			DeltaLitres: litres,
			Confidence:  conf,
		})
	}
	return out, rows.Err()
}

func nearestDropHint(hints []*fuelpkg.TelemetryDropHint, recordDate string) (*fuelpkg.TelemetryDropHint, bool) {
	if len(hints) == 0 || recordDate == "" {
		return nil, false
	}
	recDay, err := time.Parse("2006-01-02", recordDate)
	if err != nil {
		return nil, false
	}
	var best *fuelpkg.TelemetryDropHint
	bestDist := telemetryDropMatchWindow + time.Second
	for _, h := range hints {
		dist := h.TS.Sub(recDay)
		if dist < 0 {
			dist = -dist
		}
		if dist <= telemetryDropMatchWindow && dist < bestDist {
			bestDist = dist
			best = h
		}
	}
	return best, best != nil
}

func anomalyTypesEqual(a, b models.AnomalyTypes) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsType(types models.AnomalyTypes, want string) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}

func historyNoteContains(h models.AnomalyHistory, marker string) bool {
	for _, ev := range h {
		if strings.Contains(ev.Note, marker) {
			return true
		}
	}
	return false
}

// ReconcileFuelSummary returns a log line for job output.
func ReconcileFuelSummary(r ReconcileFuelResult, err error) string {
	if err != nil {
		return fmt.Sprintf("fuel reconcile failed: %v", err)
	}
	return fmt.Sprintf(
		"fuel reconcile: %d linked, %d reflagged (%d telemetry-missing, %d telemetry-mismatch, %d telemetry-drops)",
		r.Linked, r.Reflagged, r.TelemetryMissing, r.TelemetryMismatch, r.TelemetryDrops,
	)
}
