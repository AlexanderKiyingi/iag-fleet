package jmp

import (
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
)

func TestFuelReconciliationNote_overBudget(t *testing.T) {
	note := FuelReconciliationNote(FuelReconciliation{
		EstimateL:   100,
		ActualL:     140,
		VariancePct: 40,
		OverBudget:  true,
		RecordCount: 3,
	})
	if note == "" {
		t.Fatal("expected note")
	}
}

func TestJmpFuelWindowEnd_prefersCompletedAt(t *testing.T) {
	j := models.JMP{
		StartDate:      "2026-05-01",
		ExpectedReturn: "2026-05-10",
		CompletedAt:    "2026-05-15T14:30:00Z",
	}
	if got := jmpFuelWindowEnd(j); got != "2026-05-15" {
		t.Fatalf("expected completed date, got %s", got)
	}
}

func TestJmpFuelWindowEnd_fallsBackToExpectedReturn(t *testing.T) {
	j := models.JMP{
		StartDate:      "2026-05-01",
		ExpectedReturn: "2026-05-10",
	}
	if got := jmpFuelWindowEnd(j); got != "2026-05-10" {
		t.Fatalf("expected expectedReturn, got %s", got)
	}
}
