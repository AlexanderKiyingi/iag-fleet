package fuel

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/iag/fleet-tool/backend/internal/models"
)

type anomalyHit struct {
	priority int
	atype    string
	reason   string
}

// EnrichAnomaly applies all matching heuristics and sets anomaly flags on a fuel record.
func EnrichAnomaly(rec *models.FuelRecord, ctx AnomalyContext) {
	if rec == nil {
		return
	}
	base := BaseDieselPriceUGX()
	rec.Total = math.Round(rec.Litres * rec.UnitPrice)

	hits := collectAnomalyHits(rec, ctx, base)
	if len(hits) == 0 {
		t := false
		rec.Anomaly = &t
		rec.AnomalyReason = ""
		rec.AnomalyType = ""
		rec.AnomalyTypes = nil
		if rec.AnomalyStatus != "dismissed" && rec.AnomalyStatus != "resolved" {
			rec.AnomalyStatus = ""
		}
		return
	}

	sort.SliceStable(hits, func(i, j int) bool { return hits[i].priority < hits[j].priority })

	types := make(models.AnomalyTypes, len(hits))
	reasons := make([]string, len(hits))
	for i, h := range hits {
		types[i] = h.atype
		reasons[i] = h.reason
	}

	t := true
	rec.Anomaly = &t
	rec.AnomalyTypes = types
	rec.AnomalyType = hits[0].atype
	rec.AnomalyReason = strings.Join(reasons, "; ")
	if rec.AnomalyStatus == "" {
		rec.AnomalyStatus = "open"
	}
}

func collectAnomalyHits(rec *models.FuelRecord, ctx AnomalyContext, base float64) []anomalyHit {
	var hits []anomalyHit
	add := func(priority int, atype, reason string, ok bool) {
		if ok {
			hits = append(hits, anomalyHit{priority: priority, atype: atype, reason: reason})
		}
	}

	add(10, "odo-regression", "ODO lower than previous fill", checkOdoRegression(rec, ctx))
	add(20, "odo-stale", "No distance since previous fill", checkOdoStale(rec, ctx))
	add(30, "tank-overflow", fmt.Sprintf("Fill exceeds tank capacity (%d L)", ctx.TankCapacityLitres), checkTankOverflow(rec, ctx))
	if d := ctx.TelemetryDrop; d != nil && !historyHasFuelEventNote(rec, d.EventID) {
		litres := math.Abs(d.DeltaLitres)
		add(35, "telemetry-drop",
			fmt.Sprintf("Telemetry fuel drop %.1f L (event %d)", litres, d.EventID),
			d.Confidence == "high" || d.Confidence == "medium")
	}
	add(40, "telemetry-mismatch", "Ledger litres differ from telemetry refuel", checkTelemetryMismatch(rec, ctx))
	add(50, "telemetry-missing", "No telemetry refuel detected for tracked vehicle", checkTelemetryMissing(rec, ctx))
	if kmpl, _, ok := PairedKmPerLitre(ctx.PriorFill, rec); ok && checkEfficiencyLow(rec, ctx) {
		add(60, "efficiency-low", fmt.Sprintf("Low efficiency %.1f km/L vs %.1f avg", kmpl, ctx.AvgKmPerLitre), true)
	}
	if kmpl, _, ok := PairedKmPerLitre(ctx.PriorFill, rec); ok && checkEfficiencyHigh(rec, ctx) {
		add(65, "efficiency-high", fmt.Sprintf("High efficiency %.1f km/L vs %.1f avg", kmpl, ctx.AvgKmPerLitre), true)
	}
	add(70, "price-high", "Price deviation", rec.Litres > 0 && math.Abs(rec.UnitPrice-base) > 500)
	add(80, "volume-low", "Low litres", rec.Litres > 0 && rec.Litres < VolumeLowThreshold(ctx.TankCapacityLitres))
	if volHigh := VolumeHighThreshold(ctx.TankCapacityLitres); volHigh > 0 {
		add(90, "volume-high", "Large refill", rec.Litres > volHigh)
	}
	return hits
}

func historyHasFuelEventNote(rec *models.FuelRecord, eventID int64) bool {
	marker := fmt.Sprintf("fuel_event:%d", eventID)
	for _, ev := range rec.AnomalyHistory {
		if strings.Contains(ev.Note, marker) {
			return true
		}
	}
	return false
}

func checkOdoRegression(rec *models.FuelRecord, ctx AnomalyContext) bool {
	if ctx.PriorFill == nil || rec.Odo <= 0 || ctx.PriorFill.Odo <= 0 {
		return false
	}
	return rec.Odo < ctx.PriorFill.Odo
}

func checkOdoStale(rec *models.FuelRecord, ctx AnomalyContext) bool {
	if ctx.PriorFill == nil || rec.Odo <= 0 || ctx.PriorFill.Odo <= 0 {
		return false
	}
	return rec.Odo == ctx.PriorFill.Odo
}

func checkTankOverflow(rec *models.FuelRecord, ctx AnomalyContext) bool {
	if ctx.TankCapacityLitres <= 0 || rec.Litres <= 0 {
		return false
	}
	return rec.Litres > TankOverflowLitres(ctx.TankCapacityLitres)
}

func checkTelemetryMismatch(rec *models.FuelRecord, ctx AnomalyContext) bool {
	if ctx.TelemetryLitres <= 0 || rec.Litres <= 0 {
		return false
	}
	rel := math.Abs(rec.Litres-ctx.TelemetryLitres) / rec.Litres
	return rel > telemetryLitresTolerance()
}

func checkTelemetryMissing(rec *models.FuelRecord, ctx AnomalyContext) bool {
	if !ctx.CheckTelemetryMissing || !ctx.RequireTelemetry || rec.Litres < VolumeLowThreshold(ctx.TankCapacityLitres) {
		return false
	}
	if rec.FuelEventID != nil && *rec.FuelEventID > 0 {
		return false
	}
	return true
}

func checkEfficiencyLow(rec *models.FuelRecord, ctx AnomalyContext) bool {
	minSamples := efficiencyMinSamples()
	if ctx.AvgKmPerLitreSamples < minSamples || ctx.AvgKmPerLitre <= 0 {
		return false
	}
	kmpl, _, ok := PairedKmPerLitre(ctx.PriorFill, rec)
	if !ok {
		return false
	}
	return kmpl < ctx.AvgKmPerLitre*efficiencyLowRatio()
}

func checkEfficiencyHigh(rec *models.FuelRecord, ctx AnomalyContext) bool {
	minSamples := efficiencyMinSamples()
	if ctx.AvgKmPerLitreSamples < minSamples || ctx.AvgKmPerLitre <= 0 {
		return false
	}
	kmpl, _, ok := PairedKmPerLitre(ctx.PriorFill, rec)
	if !ok {
		return false
	}
	return kmpl > ctx.AvgKmPerLitre*efficiencyHighRatio()
}

// BaseDieselPriceUGX is the reference diesel price for anomaly checks and JMP budgeting.
func BaseDieselPriceUGX() float64 {
	if v := os.Getenv("FLEET_BASE_DIESEL_UGX"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			return n
		}
	}
	return 5100
}

func tankOverflowRatio() float64 { return envFloat("FLEET_FUEL_TANK_OVERFLOW_RATIO", 1.05) }

func telemetryLitresTolerance() float64 { return envFloat("FLEET_FUEL_TELEMETRY_LITRES_TOLERANCE", 0.20) }

func efficiencyLowRatio() float64 { return envFloat("FLEET_FUEL_EFFICIENCY_LOW_RATIO", 0.70) }

func efficiencyHighRatio() float64 { return envFloat("FLEET_FUEL_EFFICIENCY_HIGH_RATIO", 1.40) }

func efficiencyMinSamples() int { return envInt("FLEET_FUEL_EFFICIENCY_MIN_SAMPLES", 3) }

func efficiencyMaxSamples() int { return envInt("FLEET_FUEL_EFFICIENCY_MAX_SAMPLES", 6) }

// JmpFuelVarianceRatio is the actual÷estimate multiplier that triggers over-budget.
func JmpFuelVarianceRatio() float64 { return envFloat("FLEET_JMP_FUEL_VARIANCE_RATIO", 1.25) }

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
