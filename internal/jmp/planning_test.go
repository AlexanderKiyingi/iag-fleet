package jmp

import (
	"context"
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
)

func TestRecalculateBudget(t *testing.T) {
	j := &models.JMP{FuelEstimateL: 100, MileageRequest: 50_000}
	RecalculateBudget(j)
	if j.TotalBudgetUgx != 100*5100+50_000 {
		t.Fatalf("budget=%v", j.TotalBudgetUgx)
	}
}

func TestEnrich_fillsDistanceAndFuel(t *testing.T) {
	j := &models.JMP{DesignatedParking: "Kihihi farm gate"}
	Enrich(context.Background(), j, "", nil)
	if j.DistanceKm <= 0 {
		t.Fatalf("expected distance, got %v", j.DistanceKm)
	}
	if j.FuelEstimateL <= 0 {
		t.Fatalf("expected fuel estimate")
	}
	if j.TotalBudgetUgx <= 0 {
		t.Fatalf("expected budget")
	}
}

func TestEstimateFuelLitres_usesEngineCapacity(t *testing.T) {
	truck := &models.Vehicle{EngineCapacity: "4.0L", Type: "Truck"}
	flat := estimateFuelLitres(100, nil)
	withEngine := estimateFuelLitres(100, truck)
	if flat <= 0 || withEngine <= 0 {
		t.Fatalf("expected positive estimates flat=%v engine=%v", flat, withEngine)
	}
	// 4.0L truck band is below the blunt 32 L/100km default.
	if withEngine >= flat {
		t.Fatalf("engine-aware estimate should undercut flat default: engine=%v flat=%v", withEngine, flat)
	}
	if parseEngineLitres("380HP") != nil {
		t.Fatal("hp badges must not parse as displacement")
	}
}

func TestMatchDestination_kampala(t *testing.T) {
	_, _, ok := matchDestination("Hotel Africana basement Kampala")
	if !ok {
		t.Fatal("expected match")
	}
}
