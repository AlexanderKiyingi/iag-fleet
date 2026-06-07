package fuel

import (
	"context"
	"math"
	"time"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// TelemetryDropHint links a ledger row to a tank-drop fuel_event.
type TelemetryDropHint struct {
	EventID     int64
	TS          time.Time
	DeltaLitres float64
	Confidence  string
}

// AnomalyContext carries per-vehicle history and telemetry hints for enrichment.
type AnomalyContext struct {
	PriorFill             *models.FuelRecord
	TankCapacityLitres    int
	AvgKmPerLitre         float64
	AvgKmPerLitreSamples  int
	TelemetryLitres       float64 // litres from linked fuel_event; 0 = unknown
	RequireTelemetry      bool    // vehicle has fuel_tracker
	CheckTelemetryMissing bool    // true during reconciliation sweeps only
	TelemetryDrop         *TelemetryDropHint
}

// BuildAnomalyContext loads vehicle fuel history and tank size for one record.
func BuildAnomalyContext(ctx context.Context, repo *store.Repository, rec *models.FuelRecord) (AnomalyContext, error) {
	out := AnomalyContext{}
	if rec == nil || rec.VehicleID == "" {
		return out, nil
	}

	history, _, err := repo.Fuel.ListFiltered(ctx, store.ListFilter{
		Filters:  map[string]string{"vehicle_id": rec.VehicleID},
		Limit:    500,
		OrderBy:  "date",
		OrderAsc: true,
	})
	if err != nil {
		return out, err
	}
	history = upsertHistoryRecord(history, rec)

	out.PriorFill = PriorFill(rec, history)
	out.AvgKmPerLitre, out.AvgKmPerLitreSamples = RollingAvgKmPerLitre(history, rec.VehicleID, rec.ID, efficiencyMaxSamples())

	if veh, err := repo.Vehicles.Get(ctx, rec.VehicleID); err == nil {
		if veh.TankCapacityLitres != nil && *veh.TankCapacityLitres > 0 {
			out.TankCapacityLitres = *veh.TankCapacityLitres
		}
		out.RequireTelemetry = veh.FuelTracker
	}

	if rec.FuelEventID != nil && *rec.FuelEventID > 0 {
		out.TelemetryLitres = LoadTelemetryLitres(ctx, repo, *rec.FuelEventID)
	}

	return out, nil
}

// BuildAnomalyContextFromHistory avoids extra DB reads when the caller already
// has the vehicle's fuel rows (bulk import, reconciliation sweeps).
func BuildAnomalyContextFromHistory(rec *models.FuelRecord, history []models.FuelRecord, tankCapLitres int, requireTelemetry bool, telemetryLitres float64) AnomalyContext {
	out := AnomalyContext{
		TankCapacityLitres: tankCapLitres,
		RequireTelemetry:   requireTelemetry,
		TelemetryLitres:    telemetryLitres,
	}
	if rec == nil {
		return out
	}
	out.PriorFill = PriorFill(rec, history)
	out.AvgKmPerLitre, out.AvgKmPerLitreSamples = RollingAvgKmPerLitre(history, rec.VehicleID, rec.ID, efficiencyMaxSamples())
	return out
}

// LoadTelemetryLitres returns refuel litres for a linked fuel_event id.
func LoadTelemetryLitres(ctx context.Context, repo *store.Repository, eventID int64) float64 {
	if eventID <= 0 {
		return 0
	}
	var deltaLitres *float64
	var deltaPct float64
	var vehicleID string
	err := repo.FuelEventsPool().QueryRow(ctx, `
		SELECT vehicle_id, delta_litres, delta_pct
		FROM fuel_events
		WHERE id = $1`, eventID).Scan(&vehicleID, &deltaLitres, &deltaPct)
	if err != nil {
		return 0
	}
	if deltaLitres != nil && *deltaLitres > 0 {
		return *deltaLitres
	}
	if deltaPct > 0 && vehicleID != "" {
		if veh, err := repo.Vehicles.Get(ctx, vehicleID); err == nil && veh.TankCapacityLitres != nil {
			return float64(*veh.TankCapacityLitres) * deltaPct / 100.0
		}
	}
	return 0
}

// ApplyEnrichment runs anomaly detection and updates rec in place.
func ApplyEnrichment(rec *models.FuelRecord, ctx AnomalyContext) {
	if rec == nil {
		return
	}
	preserveStatus := rec.AnomalyStatus
	EnrichAnomaly(rec, ctx)
	if rec.Anomaly != nil && *rec.Anomaly && preserveStatus != "" &&
		(preserveStatus == "investigating" || preserveStatus == "resolved") {
		rec.AnomalyStatus = preserveStatus
	}
}

// TankOverflowLitres is the litres threshold above nominal tank capacity.
func TankOverflowLitres(tankLitres int) float64 {
	if tankLitres <= 0 {
		return 0
	}
	return float64(tankLitres) * tankOverflowRatio()
}

// VolumeLowThreshold returns the minimum expected top-up litres.
func VolumeLowThreshold(tankLitres int) float64 {
	if tankLitres > 0 {
		return math.Min(15, float64(tankLitres)*0.05)
	}
	return 15
}

// VolumeHighThreshold returns a large-refill threshold when tank size is unknown.
func VolumeHighThreshold(tankLitres int) float64 {
	if tankLitres > 0 {
		return 0 // tank-overflow rule covers known tanks
	}
	return 200
}

// UpsertHistoryRecord replaces or appends rec so enrichment uses in-memory
// values during PATCH/PUT rather than stale rows still in the DB.
func UpsertHistoryRecord(history []models.FuelRecord, rec *models.FuelRecord) []models.FuelRecord {
	return upsertHistoryRecord(history, rec)
}

func upsertHistoryRecord(history []models.FuelRecord, rec *models.FuelRecord) []models.FuelRecord {
	if rec == nil {
		return history
	}
	out := make([]models.FuelRecord, 0, len(history)+1)
	replaced := false
	for _, r := range history {
		if r.ID == rec.ID {
			out = append(out, *rec)
			replaced = true
		} else {
			out = append(out, r)
		}
	}
	if !replaced {
		out = append(out, *rec)
	}
	return out
}
