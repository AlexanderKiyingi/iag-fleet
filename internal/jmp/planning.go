package jmp

import (
	"context"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/iag/fleet-tool/backend/internal/fuel"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/routing"
)

const acpLat = -0.880
const acpLng = 30.265

var (
	engineLitresRe = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*l(?:itres?|iters?)?\b`)
	engineCcRe     = regexp.MustCompile(`(?i)(\d{3,5})\s*cc\b`)
	engineBareRe   = regexp.MustCompile(`^(\d+(?:\.\d+)?)$`)
	engineHpRe     = regexp.MustCompile(`(?i)\b(?:hp|ps|bhp|kw)\b`)
)

// Enrich fills distance, fuel estimate, and budget when missing — mirrors HAULA v7:
// totalBudgetUgx = fuelEstimateL × dieselPrice + mileageRequest.
//
// Fuel litres prefer the assigned vehicle's engine capacity / type (same bands as
// the iag-fleet form). Flat FLEET_JMP_L_PER_100KM remains the last-resort default.
func Enrich(ctx context.Context, j *models.JMP, osrmBaseURL string, vehicle *models.Vehicle) {
	if j == nil {
		return
	}
	RecalculateBudget(j)

	if j.DistanceKm <= 0 {
		km, detail := estimateDistanceKm(ctx, osrmBaseURL, j.DesignatedParking)
		if km > 0 {
			j.DistanceKm = math.Round(km*10) / 10
			if j.RouteDetail == "" {
				j.RouteDetail = detail
			}
		}
	}
	if j.FuelEstimateL <= 0 && j.DistanceKm > 0 {
		j.FuelEstimateL = math.Round(estimateFuelLitres(j.DistanceKm, vehicle)*10) / 10
	}
	RecalculateBudget(j)
}

// RecalculateBudget sets totalBudgetUgx from fuel + mileage when both inputs are known.
func RecalculateBudget(j *models.JMP) {
	if j == nil {
		return
	}
	diesel := baseDieselPriceUGX()
	if j.FuelEstimateL > 0 || j.MileageRequest > 0 {
		j.TotalBudgetUgx = math.Round(j.FuelEstimateL*diesel + j.MileageRequest)
	}
}

func estimateFuelLitres(distanceKm float64, vehicle *models.Vehicle) float64 {
	if distanceKm <= 0 {
		return 0
	}
	rate := lPer100KmDefault()
	if vehicle != nil {
		rate = resolveConsumptionRate(vehicle.EngineCapacity, vehicle.Type)
	}
	return distanceKm * rate / 100.0
}

// resolveConsumptionRate mirrors iag-fleet/src/lib/fleet/fuel-estimate.ts.
func resolveConsumptionRate(engineCapacity, vehicleType string) float64 {
	if litres := parseEngineLitres(engineCapacity); litres != nil {
		return litresPer100KmFromEngine(*litres, vehicleType)
	}
	if strings.TrimSpace(vehicleType) != "" {
		return litresPer100KmForType(vehicleType)
	}
	return lPer100KmDefault()
}

func parseEngineLitres(raw string) *float64 {
	text := strings.TrimSpace(strings.ToLower(raw))
	if text == "" {
		return nil
	}
	if m := engineLitresRe.FindStringSubmatch(text); len(m) == 2 {
		n, err := strconv.ParseFloat(m[1], 64)
		if err == nil && n > 0 && n <= 30 {
			v := round1(n)
			return &v
		}
	}
	if m := engineCcRe.FindStringSubmatch(text); len(m) == 2 {
		cc, err := strconv.ParseFloat(m[1], 64)
		if err == nil && cc >= 50 {
			v := round1(cc / 1000)
			return &v
		}
	}
	if engineHpRe.MatchString(text) {
		return nil
	}
	if m := engineBareRe.FindStringSubmatch(text); len(m) == 2 {
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil || n <= 0 {
			return nil
		}
		if n <= 20 {
			v := round1(n)
			return &v
		}
		if n >= 500 && n <= 20000 {
			v := round1(n / 1000)
			return &v
		}
	}
	return nil
}

func litresPer100KmFromEngine(engineLitres float64, vehicleType string) float64 {
	t := strings.ToLower(strings.TrimSpace(vehicleType))
	base, perLitre := 10.0, 2.8
	switch {
	case strings.Contains(t, "motor") || t == "motorcycle":
		base, perLitre = 2, 2.5
	case t == "car" || strings.Contains(t, "sedan") || strings.Contains(t, "saloon"):
		base, perLitre = 5, 2.2
	case t == "van" || strings.Contains(t, "pickup") || strings.Contains(t, "pick-up"):
		base, perLitre = 7, 2.4
	case strings.Contains(t, "trailer") || strings.Contains(t, "prime") || strings.Contains(t, "artic"):
		base, perLitre = 14, 2.6
	}
	return round1(clamp(base+engineLitres*perLitre, 3, 55))
}

func litresPer100KmForType(vehicleType string) float64 {
	t := strings.ToLower(strings.TrimSpace(vehicleType))
	switch {
	case t == "":
		return lPer100KmDefault()
	case strings.Contains(t, "motor") || strings.Contains(t, "bike") || t == "motorcycle":
		return 3.5
	case t == "car" || strings.Contains(t, "sedan") || strings.Contains(t, "saloon"):
		return 9
	case t == "van" || strings.Contains(t, "minibus"):
		return 12
	case strings.Contains(t, "pickup") || strings.Contains(t, "pick-up") || t == "pick up":
		return 13
	case strings.Contains(t, "trailer") || strings.Contains(t, "prime") || strings.Contains(t, "artic"):
		return 36
	case strings.Contains(t, "truck") || strings.Contains(t, "lorry") || strings.Contains(t, "tipper") || strings.Contains(t, "haul"):
		return 30
	default:
		return 28
	}
}

func lPer100KmDefault() float64 {
	if v := os.Getenv("FLEET_JMP_L_PER_100KM"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			return n
		}
	}
	return 32.0
}

func round1(n float64) float64 {
	return math.Round(n*10) / 10
}

func clamp(n, min, max float64) float64 {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func baseDieselPriceUGX() float64 {
	return fuel.BaseDieselPriceUGX()
}

func estimateDistanceKm(ctx context.Context, osrmBaseURL, destination string) (km float64, detail string) {
	destLat, destLng, matched := matchDestination(destination)
	if !matched {
		return 0, "distance not estimated — destination not matched to a known POI"
	}
	points := [][2]float64{{acpLat, acpLng}, {destLat, destLng}}
	base := strings.TrimSpace(osrmBaseURL)
	if base != "" {
		if rr, err := routing.RequestOSRM(ctx, base, "driving", points); err == nil && rr != nil && rr.DistanceM > 0 {
			return rr.DistanceM / 1000.0, "OSRM driving distance"
		}
	}
	_, distM := routing.StraightLinePath(points)
	if distM > 0 {
		return distM / 1000.0, "straight-line distance (OSRM unavailable)"
	}
	return 0, ""
}

// matchDestination resolves designated parking / destination text to a reference POI.
func matchDestination(destination string) (lat, lng float64, ok bool) {
	d := strings.ToLower(strings.TrimSpace(destination))
	if d == "" {
		return 0, 0, false
	}
	bestLen := 0
	for _, p := range models.POIs {
		name := strings.ToLower(p.Name)
		if strings.Contains(d, name) || strings.Contains(name, d) {
			if len(name) > bestLen {
				bestLen = len(name)
				lat, lng, ok = p.Lat, p.Lng, true
			}
		}
	}
	if ok {
		return lat, lng, true
	}
	// Token aliases used in operations copy.
	aliases := map[string][2]float64{
		"kihihi":    {-0.943, 29.989},
		"mombasa":   {-4.050, 39.667},
		"kampala":   {0.327, 32.591},
		"nairobi":   {-1.286, 36.817},
		"mbarara":   {-0.607, 30.658},
		"malaba":    {0.637, 34.265},
		"juba":      {4.853, 31.583},
		"ntungamo":  {-0.878, 30.264},
		"nyabihoko": {-0.912, 30.118},
	}
	for token, coord := range aliases {
		if strings.Contains(d, token) {
			return coord[0], coord[1], true
		}
	}
	return 0, 0, false
}

// ComputeExpectedDays mirrors the frontend / workflow helper.
func ComputeExpectedDays(startDate, endDate string) int {
	const day = 24 * time.Hour
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return 1
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return 1
	}
	d := int(end.Sub(start)/day) + 1
	if d < 1 {
		return 1
	}
	return d
}
