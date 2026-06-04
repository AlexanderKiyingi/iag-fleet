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
	Enrich(context.Background(), j, "")
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

func TestMatchDestination_kampala(t *testing.T) {
	_, _, ok := matchDestination("Hotel Africana basement Kampala")
	if !ok {
		t.Fatal("expected match")
	}
}
