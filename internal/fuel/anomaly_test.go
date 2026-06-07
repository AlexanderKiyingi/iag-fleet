package fuel

import (
	"os"
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
)

func TestEnrichAnomaly_priceHigh(t *testing.T) {
	rec := &models.FuelRecord{Litres: 100, UnitPrice: 6000}
	EnrichAnomaly(rec, AnomalyContext{})
	if rec.Anomaly == nil || !*rec.Anomaly || rec.AnomalyType != "price-high" {
		t.Fatalf("expected price-high, got %+v", rec)
	}
}

func TestEnrichAnomaly_multipleHits(t *testing.T) {
	prior := &models.FuelRecord{ID: "F1", VehicleID: "V1", Date: "2026-05-01", Odo: 1000, Litres: 50}
	rec := &models.FuelRecord{ID: "F2", VehicleID: "V1", Date: "2026-05-10", Odo: 1000, Litres: 100, UnitPrice: 6000}
	EnrichAnomaly(rec, AnomalyContext{PriorFill: prior})
	if rec.Anomaly == nil || !*rec.Anomaly {
		t.Fatal("expected anomaly")
	}
	if len(rec.AnomalyTypes) < 2 {
		t.Fatalf("expected multiple types, got %v", rec.AnomalyTypes)
	}
	if rec.AnomalyType != "odo-stale" {
		t.Fatalf("primary type should be odo-stale, got %s", rec.AnomalyType)
	}
}

func TestEnrichAnomaly_odoRegression(t *testing.T) {
	prior := &models.FuelRecord{ID: "F1", VehicleID: "V1", Date: "2026-05-01", Odo: 1000, Litres: 50}
	rec := &models.FuelRecord{ID: "F2", VehicleID: "V1", Date: "2026-05-10", Odo: 950, Litres: 50, UnitPrice: 5100}
	ctx := AnomalyContext{PriorFill: prior}
	EnrichAnomaly(rec, ctx)
	if rec.AnomalyType != "odo-regression" {
		t.Fatalf("expected odo-regression, got %s", rec.AnomalyType)
	}
}

func TestEnrichAnomaly_odoStale(t *testing.T) {
	prior := &models.FuelRecord{ID: "F1", VehicleID: "V1", Date: "2026-05-01", Odo: 1000, Litres: 50}
	rec := &models.FuelRecord{ID: "F2", VehicleID: "V1", Date: "2026-05-10", Odo: 1000, Litres: 50, UnitPrice: 5100}
	ctx := AnomalyContext{PriorFill: prior}
	EnrichAnomaly(rec, ctx)
	if rec.AnomalyType != "odo-stale" {
		t.Fatalf("expected odo-stale, got %s", rec.AnomalyType)
	}
}

func TestEnrichAnomaly_tankOverflow(t *testing.T) {
	rec := &models.FuelRecord{Litres: 220, UnitPrice: 5100}
	ctx := AnomalyContext{TankCapacityLitres: 200}
	EnrichAnomaly(rec, ctx)
	if rec.AnomalyType != "tank-overflow" {
		t.Fatalf("expected tank-overflow, got %s", rec.AnomalyType)
	}
}

func TestEnrichAnomaly_telemetryMismatch(t *testing.T) {
	rec := &models.FuelRecord{Litres: 100, UnitPrice: 5100}
	ctx := AnomalyContext{TelemetryLitres: 50}
	EnrichAnomaly(rec, ctx)
	if rec.AnomalyType != "telemetry-mismatch" {
		t.Fatalf("expected telemetry-mismatch, got %s", rec.AnomalyType)
	}
}

func TestEnrichAnomaly_telemetryMissingOnlyWhenRequested(t *testing.T) {
	rec := &models.FuelRecord{Litres: 80, UnitPrice: 5100}
	ctx := AnomalyContext{RequireTelemetry: true}
	EnrichAnomaly(rec, ctx)
	if rec.Anomaly != nil && *rec.Anomaly {
		t.Fatal("telemetry-missing should not run without CheckTelemetryMissing")
	}
	ctx.CheckTelemetryMissing = true
	EnrichAnomaly(rec, ctx)
	if rec.AnomalyType != "telemetry-missing" {
		t.Fatalf("expected telemetry-missing, got %s", rec.AnomalyType)
	}
}

func TestEnrichAnomaly_efficiencyLow(t *testing.T) {
	os.Setenv("FLEET_FUEL_EFFICIENCY_MIN_SAMPLES", "2")
	defer os.Unsetenv("FLEET_FUEL_EFFICIENCY_MIN_SAMPLES")

	prior := &models.FuelRecord{ID: "F1", VehicleID: "V1", Date: "2026-05-01", Odo: 1000, Litres: 50, UnitPrice: 5100}
	rec := &models.FuelRecord{ID: "F2", VehicleID: "V1", Date: "2026-05-10", Odo: 1100, Litres: 100, UnitPrice: 5100}
	// prior interval: 100km/50L = 2 km/L; current: 100km/100L = 1 km/L (< 70% of 2)
	ctx := AnomalyContext{
		PriorFill:            prior,
		AvgKmPerLitre:        2.0,
		AvgKmPerLitreSamples: 3,
	}
	EnrichAnomaly(rec, ctx)
	if rec.AnomalyType != "efficiency-low" {
		t.Fatalf("expected efficiency-low, got %s (%s)", rec.AnomalyType, rec.AnomalyReason)
	}
}

func TestPriorFill_andRollingAvg(t *testing.T) {
	history := []models.FuelRecord{
		{ID: "A", VehicleID: "V1", Date: "2026-05-01", Odo: 1000, Litres: 50},
		{ID: "B", VehicleID: "V1", Date: "2026-05-10", Odo: 1100, Litres: 50},
		{ID: "C", VehicleID: "V1", Date: "2026-05-20", Odo: 1200, Litres: 50},
	}
	cur := &models.FuelRecord{ID: "C", VehicleID: "V1", Date: "2026-05-20", Odo: 1200, Litres: 50}
	prior := PriorFill(cur, history)
	if prior == nil || prior.ID != "B" {
		t.Fatalf("expected prior B, got %v", prior)
	}
	avg, n := RollingAvgKmPerLitre(history, "V1", "", 6)
	if n != 2 || avg != 2.0 {
		t.Fatalf("avg=%v samples=%d", avg, n)
	}
}
