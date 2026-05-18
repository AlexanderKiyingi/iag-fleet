package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/cache"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Analytics owns the /api/analytics/summary aggregator. Mirrors the shape
// the /analytics page used to derive client-side from the full vehicle /
// fuel / driver / trip / request / compliance lists. Putting the math in
// the backend keeps the page snappy as the dataset grows past a few k
// fuel rows (the client-side O(n²) groupings start to bite around there).
type Analytics struct {
	Repo  *store.Repository
	Cache cache.Cache
	TTL   time.Duration
}

func (a *Analytics) Register(rg *gin.RouterGroup) {
	rg.GET("/analytics/summary", auth.RequireUser(), a.summary)
}

// ────────────────────── Response shape ──────────────────────────────

type analyticsKpis struct {
	VehiclesTotal int     `json:"vehiclesTotal"`
	Moving        int     `json:"moving"`
	UtilPct       int     `json:"utilPct"`
	TotalOdo      float64 `json:"totalOdo"`
	// Cost/km uses (sum of fuel.total) / (sum of vehicle ODO deltas) over
	// the last 30 days. Cost/t-km is the same divided by an assumed 8-tonne
	// average payload — same heuristic the v4 analytics screen used.
	CostPerKm  int     `json:"costPerKm"`
	CostPerTKm int     `json:"costPerTKm"`
	FuelCost30d float64 `json:"fuelCost30d"`
}

type analyticsDayBucket struct {
	Date string  `json:"date"` // YYYY-MM-DD
	UGX  float64 `json:"ugx"`
}

type analyticsDriverScore struct {
	DriverID    string  `json:"driverId"`
	Name        string  `json:"name"`
	Trips       int     `json:"trips"`
	LPer100km   float64 `json:"lPer100km"`   // 0 when no fuel/distance recorded
	SafetyScore float64 `json:"safetyScore"`
}

type analyticsFuelEff struct {
	VehicleID string  `json:"vehicleId"`
	Plate     string  `json:"plate"`
	LPer100km float64 `json:"lPer100km"`
}

type analyticsDeptCount struct {
	Department string `json:"department"`
	Count      int    `json:"count"`
}

type analyticsCompliance struct {
	Valid   int `json:"valid"`
	Expiring int `json:"expiring"`
	AtRisk  int `json:"atRisk"`
}

type analyticsSummary struct {
	GeneratedAt    string                 `json:"generatedAt"`
	Kpis           analyticsKpis          `json:"kpis"`
	Daily14d       []analyticsDayBucket   `json:"daily14d"`
	DriverScores   []analyticsDriverScore `json:"driverScores"`
	FuelEfficiency []analyticsFuelEff     `json:"fuelEfficiency"`
	RequestsByDept []analyticsDeptCount   `json:"requestsByDept"`
	Compliance     analyticsCompliance    `json:"compliance"`
}

// ────────────────────── Handler ─────────────────────────────────────

func (a *Analytics) summary(c *gin.Context) {
	ctx := c.Request.Context()

	if a.Cache != nil && a.TTL > 0 {
		if blob, ok, _ := a.Cache.Get(ctx, cache.KeyAnalytics); ok && len(blob) > 0 {
			c.Data(http.StatusOK, "application/json", blob)
			return
		}
	}

	vehicles, err := a.Repo.Vehicles.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	drivers, err := a.Repo.Drivers.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	trips, err := a.Repo.Trips.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	fuel, err := a.Repo.Fuel.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	requests, err := a.Repo.Requests.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	compliance, err := a.Repo.Compliance.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	out := analyticsSummary{GeneratedAt: nowISO()}
	out.Kpis = computeAnalyticsKpis(vehicles, fuel)
	out.Daily14d = computeDaily14d(fuel)
	out.DriverScores = computeDriverScores(drivers, trips)
	out.FuelEfficiency = computeFuelEfficiency(vehicles, fuel)
	out.RequestsByDept = computeRequestsByDept(requests)
	out.Compliance = computeComplianceHealth(compliance)

	blob, err := json.Marshal(out)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if a.Cache != nil && a.TTL > 0 {
		_ = a.Cache.Set(ctx, cache.KeyAnalytics, blob, a.TTL)
	}
	c.Data(http.StatusOK, "application/json", blob)
}

// ────────────────────── Aggregation helpers ─────────────────────────

func computeAnalyticsKpis(vehicles []models.Vehicle, fuel []models.FuelRecord) analyticsKpis {
	k := analyticsKpis{VehiclesTotal: len(vehicles)}
	for _, v := range vehicles {
		k.TotalOdo += v.Odo
		if v.Status == "moving" {
			k.Moving++
		}
	}
	if len(vehicles) > 0 {
		k.UtilPct = int((float64(k.Moving) / float64(len(vehicles))) * 100.0)
	}

	// 30-day cost/km: take the last 30 days of fuel records, sum their
	// total cost, divide by the per-vehicle ODO delta (last - first
	// record). Vehicles with only one record contribute cost but no km;
	// we count both globally so partial coverage doesn't blow up the math.
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	type window struct {
		first, last float64
		hasFirst    bool
	}
	byVeh := make(map[string]*window)
	totalCost := 0.0
	// We need the records sorted by date so first/last reflect the window
	// ends, not insertion order. Sorting in place is fine — we don't reuse
	// the slice afterwards.
	sort.SliceStable(fuel, func(i, j int) bool { return fuel[i].Date < fuel[j].Date })
	for _, f := range fuel {
		if f.Date < cutoff {
			continue
		}
		totalCost += f.Total
		w := byVeh[f.VehicleID]
		if w == nil {
			w = &window{}
			byVeh[f.VehicleID] = w
		}
		if !w.hasFirst {
			w.first = f.Odo
			w.hasFirst = true
		}
		w.last = f.Odo
	}
	totalKm := 0.0
	for _, w := range byVeh {
		if w.hasFirst && w.last > w.first {
			totalKm += w.last - w.first
		}
	}
	k.FuelCost30d = totalCost
	if totalKm > 0 {
		k.CostPerKm = int(totalCost / totalKm)
		// 8-tonne assumed payload — same heuristic the v4 spreadsheet used.
		k.CostPerTKm = int(totalCost / (totalKm * 8))
	}
	return k
}

func computeDaily14d(fuel []models.FuelRecord) []analyticsDayBucket {
	out := make([]analyticsDayBucket, 14)
	now := time.Now().UTC()
	idx := make(map[string]int, 14)
	for i := range out {
		d := now.AddDate(0, 0, -(13 - i)).Format("2006-01-02")
		out[i] = analyticsDayBucket{Date: d}
		idx[d] = i
	}
	for _, f := range fuel {
		if i, ok := idx[f.Date]; ok {
			out[i].UGX += f.Total
		}
	}
	return out
}

func computeDriverScores(drivers []models.Driver, trips []models.Trip) []analyticsDriverScore {
	type agg struct {
		trips     int
		distance  float64
		fuel      float64
	}
	byDriver := make(map[string]*agg, len(drivers))
	for _, t := range trips {
		a := byDriver[t.DriverID]
		if a == nil {
			a = &agg{}
			byDriver[t.DriverID] = a
		}
		a.trips++
		a.distance += t.DistanceKm
		a.fuel += t.FuelL
	}
	out := make([]analyticsDriverScore, 0, len(drivers))
	for _, d := range drivers {
		// External (transporter) drivers are ranked separately — match the
		// frontend's existing exclusion so the leaderboard stays IAG-only.
		if d.External {
			continue
		}
		a := byDriver[d.ID]
		score := analyticsDriverScore{
			DriverID:    d.ID,
			Name:        d.Name,
			SafetyScore: d.SafetyScore,
		}
		if a != nil {
			score.Trips = a.trips
			if a.distance > 0 {
				// L/100km, rounded to one decimal at format time.
				score.LPer100km = a.fuel / a.distance * 100
			}
		}
		out = append(out, score)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SafetyScore > out[j].SafetyScore })
	return out
}

func computeFuelEfficiency(vehicles []models.Vehicle, fuel []models.FuelRecord) []analyticsFuelEff {
	// Per-vehicle totals: sum litres, track first/last ODO seen. Sort by
	// date first so first/last reflect chronological ordering rather than
	// insertion order.
	sort.SliceStable(fuel, func(i, j int) bool { return fuel[i].Date < fuel[j].Date })

	type windowF struct {
		litres   float64
		first    float64
		last     float64
		hasFirst bool
	}
	byVeh := make(map[string]*windowF)
	for _, f := range fuel {
		w := byVeh[f.VehicleID]
		if w == nil {
			w = &windowF{}
			byVeh[f.VehicleID] = w
		}
		w.litres += f.Litres
		if f.Odo > 0 {
			if !w.hasFirst {
				w.first = f.Odo
				w.hasFirst = true
			}
			w.last = f.Odo
		}
	}
	plateByID := make(map[string]string, len(vehicles))
	for _, v := range vehicles {
		plateByID[v.ID] = v.Plate
	}
	out := make([]analyticsFuelEff, 0, len(byVeh))
	for vid, w := range byVeh {
		plate, ok := plateByID[vid]
		if !ok {
			continue // orphan — vehicle was deleted but fuel rows survive
		}
		if !w.hasFirst || w.last <= w.first {
			continue // not enough ODO data to compute efficiency
		}
		km := w.last - w.first
		out = append(out, analyticsFuelEff{
			VehicleID: vid,
			Plate:     plate,
			LPer100km: w.litres / km * 100,
		})
	}
	// Worst → best: highest L/100km first, so operators see thirstiest
	// vehicles at the top of the bar chart.
	sort.SliceStable(out, func(i, j int) bool { return out[i].LPer100km > out[j].LPer100km })
	return out
}

func computeRequestsByDept(requests []models.ServiceRequest) []analyticsDeptCount {
	counts := make(map[string]int)
	for _, r := range requests {
		counts[r.RequesterDept]++
	}
	out := make([]analyticsDeptCount, 0, len(counts))
	for dept, n := range counts {
		out = append(out, analyticsDeptCount{Department: dept, Count: n})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func computeComplianceHealth(items []models.ComplianceItem) analyticsCompliance {
	var c analyticsCompliance
	for _, it := range items {
		switch it.Status {
		case "valid":
			c.Valid++
		case "expiring":
			c.Expiring++
		case "expired", "missing":
			c.AtRisk++
		}
	}
	return c
}
