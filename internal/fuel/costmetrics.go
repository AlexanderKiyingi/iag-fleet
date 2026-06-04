package fuel

import (
	"sort"
	"time"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// CostMetrics is the canonical fleet cost-per-distance rollup from manual fuel_records.
// Distance is derived from per-vehicle ODO deltas (first→last fuel row in the window),
// not trip.distanceKm, so analytics and reports stay aligned.
type CostMetrics struct {
	TotalCost    float64 `json:"totalCost"`
	TotalKm      float64 `json:"totalKm"`
	CostPerKm    int     `json:"costPerKm"`
	CostPerTKm   int     `json:"costPerTKm"`
	PayloadTonnes float64 `json:"payloadTonnes"`
}

// DefaultPayloadTonnes matches the v4/v7 analytics heuristic (8-tonne average load).
const DefaultPayloadTonnes = 8.0

// ComputeCostMetrics aggregates fuel spend and ODO-based km for records with date >= cutoff (YYYY-MM-DD).
// Records are sorted by date before computing first/last ODO per vehicle.
func ComputeCostMetrics(fuel []models.FuelRecord, cutoff string, payloadTonnes float64) CostMetrics {
	if payloadTonnes <= 0 {
		payloadTonnes = DefaultPayloadTonnes
	}
	out := CostMetrics{PayloadTonnes: payloadTonnes}

	type window struct {
		first, last float64
		hasFirst    bool
	}
	byVeh := make(map[string]*window)
	sorted := append([]models.FuelRecord(nil), fuel...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })

	for _, f := range sorted {
		if cutoff != "" && f.Date < cutoff {
			continue
		}
		out.TotalCost += f.Total
		w := byVeh[f.VehicleID]
		if w == nil {
			w = &window{}
			byVeh[f.VehicleID] = w
		}
		if f.Odo > 0 {
			if !w.hasFirst {
				w.first = f.Odo
				w.hasFirst = true
			}
			w.last = f.Odo
		}
	}
	for _, w := range byVeh {
		if w.hasFirst && w.last > w.first {
			out.TotalKm += w.last - w.first
		}
	}
	if out.TotalKm > 0 {
		out.CostPerKm = int(out.TotalCost / out.TotalKm)
		out.CostPerTKm = int(out.TotalCost / (out.TotalKm * payloadTonnes))
	}
	return out
}

// CutoffDaysAgo returns a UTC YYYY-MM-DD cutoff n days before today.
func CutoffDaysAgo(days int) string {
	return time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
}
