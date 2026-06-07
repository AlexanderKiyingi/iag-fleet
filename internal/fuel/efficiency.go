package fuel

import (
	"sort"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// PriorFill returns the chronologically previous fuel row for the same vehicle
// (excluding rec itself). Nil when none exists.
func PriorFill(rec *models.FuelRecord, history []models.FuelRecord) *models.FuelRecord {
	if rec == nil || rec.VehicleID == "" {
		return nil
	}
	sorted := vehicleHistory(history, rec.VehicleID)
	var prior *models.FuelRecord
	for i := range sorted {
		r := &sorted[i]
		if r.ID == rec.ID {
			break
		}
		if rec.Date != "" && r.Date > rec.Date {
			continue
		}
		if rec.Date != "" && r.Date == rec.Date && r.ID >= rec.ID {
			continue
		}
		prior = r
	}
	return prior
}

// PairedKmPerLitre computes km/L for the interval ending at current (litres
// attributed to current fill). Returns ok=false when ODO or litres are missing.
func PairedKmPerLitre(prior, current *models.FuelRecord) (kmPerL, distance float64, ok bool) {
	if prior == nil || current == nil {
		return 0, 0, false
	}
	if prior.Odo <= 0 || current.Odo <= 0 || current.Litres <= 0 {
		return 0, 0, false
	}
	distance = current.Odo - prior.Odo
	if distance <= 0 {
		return 0, distance, false
	}
	return distance / current.Litres, distance, true
}

// RollingAvgKmPerLitre averages paired km/L across prior fills for one vehicle,
// excluding excludeID (typically the record being evaluated).
func RollingAvgKmPerLitre(history []models.FuelRecord, vehicleID, excludeID string, maxSamples int) (avg float64, samples int) {
	if maxSamples <= 0 {
		maxSamples = 6
	}
	sorted := vehicleHistory(history, vehicleID)
	var pairs []float64
	for i := 1; i < len(sorted); i++ {
		cur := &sorted[i]
		if cur.ID == excludeID {
			continue
		}
		if kmpl, _, ok := PairedKmPerLitre(&sorted[i-1], cur); ok {
			pairs = append(pairs, kmpl)
		}
	}
	if len(pairs) == 0 {
		return 0, 0
	}
	if len(pairs) > maxSamples {
		pairs = pairs[len(pairs)-maxSamples:]
	}
	var sum float64
	for _, p := range pairs {
		sum += p
	}
	return sum / float64(len(pairs)), len(pairs)
}

func vehicleHistory(history []models.FuelRecord, vehicleID string) []models.FuelRecord {
	out := make([]models.FuelRecord, 0, len(history))
	for _, r := range history {
		if r.VehicleID == vehicleID {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].ID < out[j].ID
	})
	return out
}
