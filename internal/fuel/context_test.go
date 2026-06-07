package fuel

import (
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
)

func TestUpsertHistoryRecord_replacesStaleRow(t *testing.T) {
	history := []models.FuelRecord{
		{ID: "F1", VehicleID: "V1", Date: "2026-05-01", Odo: 1000, Litres: 50},
		{ID: "F2", VehicleID: "V1", Date: "2026-05-10", Odo: 1100, Litres: 50},
	}
	merged := &models.FuelRecord{ID: "F2", VehicleID: "V1", Date: "2026-05-10", Odo: 1050, Litres: 50, UnitPrice: 5100}
	h := upsertHistoryRecord(history, merged)

	prior := PriorFill(merged, h)
	if prior == nil || prior.ID != "F1" {
		t.Fatalf("expected prior F1, got %v", prior)
	}
	kmpl, _, ok := PairedKmPerLitre(prior, merged)
	if !ok || kmpl != 1.0 {
		t.Fatalf("expected 1.0 km/L from patched odo, got %v ok=%v", kmpl, ok)
	}
}
