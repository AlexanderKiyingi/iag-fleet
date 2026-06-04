package fuel

import (
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
)

func TestComputeCostMetrics_odoDelta(t *testing.T) {
	records := []models.FuelRecord{
		{VehicleID: "V1", Date: "2026-05-01", Total: 100_000, Odo: 1000, Litres: 20},
		{VehicleID: "V1", Date: "2026-05-10", Total: 150_000, Odo: 1100, Litres: 30},
		{VehicleID: "V2", Date: "2026-05-05", Total: 50_000, Odo: 500, Litres: 10},
	}
	m := ComputeCostMetrics(records, "2026-05-01", 8)
	if m.TotalCost != 300_000 {
		t.Fatalf("totalCost=%v", m.TotalCost)
	}
	if m.TotalKm != 100 { // V1: 100km; V2: single point → no km
		t.Fatalf("totalKm=%v", m.TotalKm)
	}
	if m.CostPerKm != 3000 {
		t.Fatalf("costPerKm=%d", m.CostPerKm)
	}
}
