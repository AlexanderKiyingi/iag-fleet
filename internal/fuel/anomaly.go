package fuel

import (
	"math"
	"os"
	"strconv"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// EnrichAnomaly applies v7-style heuristics and sets anomaly flags on a fuel record.
func EnrichAnomaly(rec *models.FuelRecord) {
	if rec == nil {
		return
	}
	base := baseDieselPrice()
	rec.Total = math.Round(rec.Litres * rec.UnitPrice)

	anomaly := false
	reason := ""
	atype := ""

	if rec.Litres > 0 && math.Abs(rec.UnitPrice-base) > 500 {
		anomaly = true
		reason = "Price deviation"
		atype = "price-high"
	} else if rec.Litres > 0 && rec.Litres < 15 {
		anomaly = true
		reason = "Low litres"
		atype = "volume-low"
	} else if rec.Litres > 200 {
		anomaly = true
		reason = "Large refill"
		atype = "volume-high"
	}

	rec.Anomaly = &anomaly
	if anomaly {
		rec.AnomalyReason = reason
		rec.AnomalyType = atype
		if rec.AnomalyStatus == "" {
			rec.AnomalyStatus = "open"
		}
	} else {
		rec.AnomalyReason = ""
		rec.AnomalyType = ""
		rec.AnomalyStatus = ""
	}
}

func baseDieselPrice() float64 {
	if v := os.Getenv("FLEET_BASE_DIESEL_UGX"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			return n
		}
	}
	return 5100
}
