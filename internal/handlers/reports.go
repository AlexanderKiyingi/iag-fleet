package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Reports exposes aggregated report rollups (v7 drawReports parity).
type Reports struct {
	Repo *store.Repository
}

func (r *Reports) Register(rg *gin.RouterGroup) {
	rg.GET("/reports/summary", auth.RequireUser(), r.summary)
}

type reportBucket struct {
	Label   string  `json:"label"`
	Litres  float64 `json:"litres"`
	SpendUgx float64 `json:"spendUgx"`
	Count   int     `json:"count"`
}

type reportSummary struct {
	GeneratedAt    string         `json:"generatedAt"`
	Period         string         `json:"period"`
	Scope          string         `json:"scope"`
	ScopeID        string         `json:"scopeId,omitempty"`
	WindowStart    string         `json:"windowStart"`
	WindowEnd      string         `json:"windowEnd"`
	TotalKm        int            `json:"totalKm"`
	TotalLitres    float64        `json:"totalLitres"`
	TotalSpendUgx  float64        `json:"totalSpendUgx"`
	CostPerKmUgx   int            `json:"costPerKmUgx"`
	MaintenanceUgx float64        `json:"maintenanceUgx"`
	WorkOrders     int            `json:"workOrders"`
	Safety         map[string]int `json:"safetyBySeverity"`
	CargoCompleted int            `json:"cargoCompleted"`
	CargoActive    int            `json:"cargoActive"`
	VehicleStatus  map[string]int `json:"vehicleStatus"`
	FuelBuckets    []reportBucket `json:"fuelBuckets"`
}

func (r *Reports) summary(c *gin.Context) {
	ctx := c.Request.Context()
	period := c.DefaultQuery("period", "daily")
	scope := c.DefaultQuery("scope", "fleet")
	scopeID := c.Query("scopeId")

	now := time.Now().UTC()
	var start time.Time
	var buckets int
	var bucketDays int
	switch period {
	case "weekly":
		start = now.AddDate(0, 0, -83)
		buckets = 12
		bucketDays = 7
	case "monthly":
		start = now.AddDate(0, -11, 0)
		buckets = 12
		bucketDays = 30
	default:
		period = "daily"
		start = now.AddDate(0, 0, -13)
		buckets = 14
		bucketDays = 1
	}

	vehicles, _ := r.Repo.Vehicles.List(ctx)
	fuel, _ := r.Repo.Fuel.List(ctx)
	mx, _ := r.Repo.Maintenance.List(ctx)
	safety, _ := r.Repo.Safety.List(ctx)
	cargo, _ := r.Repo.Cargo.List(ctx)
	trips, _ := r.Repo.Trips.List(ctx)

	vehByID := map[string]models.Vehicle{}
	for _, v := range vehicles {
		vehByID[v.ID] = v
	}

	inScopeVehicle := func(vehicleID string) bool {
		switch scope {
		case "fleet":
			return true
		case "vehicle":
			return scopeID == "" || vehicleID == scopeID
		case "ownership":
			v := vehByID[vehicleID]
			return scopeID == "" || v.Ownership == scopeID
		default:
			return true
		}
	}

	fuelBuckets := make([]reportBucket, buckets)
	var totalL, totalU float64
	for _, f := range fuel {
		if !inScopeVehicle(f.VehicleID) {
			continue
		}
		if scope == "driver" && scopeID != "" && f.DriverID != scopeID {
			continue
		}
		d, err := time.Parse("2006-01-02", f.Date)
		if err != nil || d.Before(start) {
			continue
		}
		idx := int(d.Sub(start).Hours() / 24 / float64(bucketDays))
		if idx < 0 || idx >= buckets {
			continue
		}
		fuelBuckets[idx].Litres += f.Litres
		fuelBuckets[idx].SpendUgx += f.Total
		fuelBuckets[idx].Count++
		totalL += f.Litres
		totalU += f.Total
	}
	for i := range fuelBuckets {
		d := start.AddDate(0, 0, i*bucketDays)
		fuelBuckets[i].Label = d.Format("02 Jan")
	}

	var totalKm float64
	for _, t := range trips {
		if !inScopeVehicle(t.VehicleID) {
			continue
		}
		totalKm += t.DistanceKm
	}
	totalKmInt := int(totalKm)
	cpkm := 0
	if totalKmInt > 0 {
		cpkm = int(totalU / float64(totalKmInt))
	}

	statusCount := map[string]int{"moving": 0, "idle": 0, "maintenance": 0, "offline": 0}
	for _, v := range vehicles {
		if scope == "vehicle" && scopeID != "" && v.ID != scopeID {
			continue
		}
		if scope == "ownership" && scopeID != "" && v.Ownership != scopeID {
			continue
		}
		statusCount[v.Status]++
	}

	sev := map[string]int{"crit": 0, "warn": 0, "info": 0}
	var mxCost float64
	wo := 0
	for _, s := range safety {
		d, err := time.Parse(time.RFC3339, s.Date)
		if err != nil {
			d, _ = time.Parse("2006-01-02", s.Date)
		}
		if d.Before(start) || !inScopeVehicle(s.VehicleID) {
			continue
		}
		if scope == "driver" && scopeID != "" && s.DriverID != scopeID {
			continue
		}
		sev[s.Severity]++
	}
	for _, m := range mx {
		d, err := time.Parse("2006-01-02", m.Date)
		if err != nil || d.Before(start) || !inScopeVehicle(m.VehicleID) {
			continue
		}
		mxCost += m.Cost
		wo++
	}

	cargoDone, cargoActive := 0, 0
	for _, cg := range cargo {
		if cg.Stage == "completed" || cg.Stage == "demobilised" {
			cargoDone++
		} else {
			cargoActive++
		}
	}

	out := reportSummary{
		GeneratedAt:    now.Format(time.RFC3339),
		Period:         period,
		Scope:          scope,
		ScopeID:        scopeID,
		WindowStart:    start.Format("2006-01-02"),
		WindowEnd:      now.Format("2006-01-02"),
		TotalKm:        totalKmInt,
		TotalLitres:    totalL,
		TotalSpendUgx:  totalU,
		CostPerKmUgx:   cpkm,
		MaintenanceUgx: mxCost,
		WorkOrders:     wo,
		Safety:         sev,
		CargoCompleted: cargoDone,
		CargoActive:    cargoActive,
		VehicleStatus:  statusCount,
		FuelBuckets:    fuelBuckets,
	}
	c.JSON(http.StatusOK, out)
}
