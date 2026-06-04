package jmp

import (
	"context"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/iag/fleet-tool/backend/internal/fuel"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/routing"
)

const acpLat = -0.880
const acpLng = 30.265

// Enrich fills distance, fuel estimate, and budget when missing — mirrors HAULA v7:
// totalBudgetUgx = fuelEstimateL × dieselPrice + mileageRequest.
func Enrich(ctx context.Context, j *models.JMP, osrmBaseURL string) {
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
		j.FuelEstimateL = math.Round(estimateFuelLitres(j.DistanceKm)*10) / 10
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

func estimateFuelLitres(distanceKm float64) float64 {
	if distanceKm <= 0 {
		return 0
	}
	return distanceKm * lPer100Km() / 100.0
}

func lPer100Km() float64 {
	if v := os.Getenv("FLEET_JMP_L_PER_100KM"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			return n
		}
	}
	return 32.0
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
