package jmp

import (
	"context"
	"fmt"
	"strings"

	"github.com/iag/fleet-tool/backend/internal/fuel"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// FuelReconciliation compares actual fuel_records spend in the JMP window to the estimate.
type FuelReconciliation struct {
	EstimateL   float64 `json:"estimateL"`
	ActualL     float64 `json:"actualL"`
	VariancePct float64 `json:"variancePct"`
	OverBudget  bool    `json:"overBudget"`
	RecordCount int     `json:"recordCount"`
}

// ReconcileFuelActuals sums ledger litres for the JMP vehicle between start and end dates.
func ReconcileFuelActuals(ctx context.Context, repo *store.Repository, j models.JMP) (FuelReconciliation, error) {
	out := FuelReconciliation{EstimateL: j.FuelEstimateL}
	if j.VehicleID == "" || j.StartDate == "" {
		return out, nil
	}
	end := jmpFuelWindowEnd(j)

	records, _, err := repo.Fuel.ListFiltered(ctx, store.ListFilter{
		Filters: map[string]string{"vehicle_id": j.VehicleID},
		Limit:   1000,
		OrderBy: "date",
	})
	if err != nil {
		return out, err
	}

	for _, r := range records {
		if r.Date < j.StartDate || r.Date > end {
			continue
		}
		out.ActualL += r.Litres
		out.RecordCount++
	}

	if j.FuelEstimateL > 0 {
		out.VariancePct = (out.ActualL - j.FuelEstimateL) / j.FuelEstimateL * 100
		out.OverBudget = out.ActualL > j.FuelEstimateL*fuel.JmpFuelVarianceRatio()
	}
	return out, nil
}

func jmpFuelWindowEnd(j models.JMP) string {
	if d := datePrefix(j.CompletedAt); d != "" {
		return d
	}
	if j.ExpectedReturn != "" {
		return j.ExpectedReturn
	}
	if j.ExpectedArrival != "" {
		return j.ExpectedArrival
	}
	return j.StartDate
}

func datePrefix(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ""
}

// FuelReconciliationNote formats an audit-log friendly summary.
func FuelReconciliationNote(r FuelReconciliation) string {
	if r.EstimateL <= 0 {
		return fmt.Sprintf("actual fuel %.0f L across %d records (no estimate on JMP)", r.ActualL, r.RecordCount)
	}
	return strings.TrimSpace(fmt.Sprintf(
		"fuel actual %.0f L vs estimate %.0f L (%.0f%%); %d records%s",
		r.ActualL, r.EstimateL, r.VariancePct, r.RecordCount,
		func() string {
			if r.OverBudget {
				return " · over budget"
			}
			return ""
		}(),
	))
}
