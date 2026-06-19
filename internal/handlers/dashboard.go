package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/cache"
	"github.com/iag/fleet-tool/backend/internal/jobs"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Dashboard exposes pre-computed rollups for the command-center view.
// The shape mirrors what app/(shell)/dashboard/page.tsx currently derives
// client-side, so the frontend can swap a single fetch in for ~80 lines of
// list filtering / counting logic.
type Dashboard struct {
	Repo  *store.Repository
	Cache cache.Cache
	TTL   time.Duration // 0 = disable cache layer (use NoOp from router)
}

func (d *Dashboard) Register(rg *gin.RouterGroup) {
	rg.GET("/dashboard/summary", auth.RequireAnyFleetView(), d.summary)
}

type dashboardKpis struct {
	VehiclesTotal int     `json:"vehiclesTotal"`
	Moving        int     `json:"moving"`
	UtilPct       int     `json:"utilPct"`
	OfflineOrMx   int     `json:"offlineOrMaintenance"`
	JmpsActive    int     `json:"jmpsActive"`
	JmpsPending   int     `json:"jmpsPendingToolbox"`
	RequestsOpen  int     `json:"requestsOpen"`
	CargoAtAcp    int     `json:"cargoAtAcp"`
	CargoAtMalaba int     `json:"cargoAtMalaba"`
	CargoCompleted int    `json:"cargoCompleted"`
	FuelMtdUgx    float64 `json:"fuelMtdUgx"`
	FuelAnomalies int     `json:"fuelAnomalies"`
	ComplianceAtRisk int  `json:"complianceAtRisk"`
	PmDue            int  `json:"pmDue"`
	MaintenanceOverdue int `json:"maintenanceOverdue"`
}

type dashboardAlert struct {
	Severity string `json:"severity"` // crit | warn
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	When     string `json:"when,omitempty"`
	Href     string `json:"href,omitempty"`
}

type cargoPipelineNode struct {
	Stage string `json:"stage"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type dashboardSummary struct {
	GeneratedAt   string              `json:"generatedAt"`
	Kpis          dashboardKpis       `json:"kpis"`
	CargoPipeline []cargoPipelineNode `json:"cargoPipeline"`
	Alerts        []dashboardAlert    `json:"alerts"`
}

func (d *Dashboard) summary(c *gin.Context) {
	ctx := c.Request.Context()

	if d.Cache != nil && d.TTL > 0 {
		if blob, ok, _ := d.Cache.Get(ctx, cache.KeyDashboard); ok && len(blob) > 0 {
			c.Data(http.StatusOK, "application/json", blob)
			return
		}
	}

	_, _ = d.Repo.RecomputeComplianceStatuses(ctx)

	vehicles, err := d.Repo.Vehicles.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	jmps, err := d.Repo.JMPs.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	requests, err := d.Repo.Requests.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cargo, err := d.Repo.Cargo.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	fuel, err := d.Repo.Fuel.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	compliance, err := d.Repo.Compliance.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	safety, err := d.Repo.Safety.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	drivers, err := d.Repo.Drivers.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	driverByID := make(map[string]models.Driver, len(drivers))
	for _, dr := range drivers {
		driverByID[dr.ID] = dr
	}
	vehByID := make(map[string]models.Vehicle, len(vehicles))
	for _, v := range vehicles {
		vehByID[v.ID] = v
	}

	moving := 0
	offlineOrMx := 0
	for _, v := range vehicles {
		switch v.Status {
		case "moving":
			moving++
		case "offline", "maintenance":
			offlineOrMx++
		}
	}

	kpi := dashboardKpis{VehiclesTotal: len(vehicles), Moving: moving, OfflineOrMx: offlineOrMx}
	if len(vehicles) > 0 {
		kpi.UtilPct = int((float64(moving) / float64(len(vehicles))) * 100.0)
	}

	for _, j := range jmps {
		switch j.Status {
		case "active":
			kpi.JmpsActive++
		case "pending-toolbox":
			kpi.JmpsPending++
		}
	}

	for _, r := range requests {
		if r.Status == "submitted" || r.Status == "reviewed" {
			kpi.RequestsOpen++
		}
	}

	monthPrefix := time.Now().UTC().Format("2006-01")
	for _, f := range fuel {
		if strings.HasPrefix(f.Date, monthPrefix) {
			kpi.FuelMtdUgx += f.Total
		}
		if f.Anomaly != nil && *f.Anomaly {
			kpi.FuelAnomalies++
		}
	}

	for _, cm := range compliance {
		if cm.Status != "valid" {
			kpi.ComplianceAtRisk++
		}
	}

	pmDue, _ := d.Repo.ListDuePMSchedules(ctx, jobs.DefaultPMWithinDays, jobs.DefaultPMWithinKm)
	kpi.PmDue = len(pmDue)

	maintenance, _ := d.Repo.Maintenance.List(ctx)
	today := time.Now().UTC().Format("2006-01-02")
	for _, mx := range maintenance {
		if (mx.Status == "scheduled" || mx.Status == "overdue") && mx.Date != "" && mx.Date < today {
			kpi.MaintenanceOverdue++
		}
	}

	pipeline := make([]cargoPipelineNode, 0, len(models.CargoStages))
	stageCount := make(map[string]int, len(models.CargoStages))
	for _, s := range models.CargoStages {
		stageCount[s.K] = 0
	}
	for _, cg := range cargo {
		stageCount[cg.Stage]++
	}
	for _, s := range models.CargoStages {
		pipeline = append(pipeline, cargoPipelineNode{Stage: s.K, Label: s.Label, Count: stageCount[s.K]})
	}
	kpi.CargoAtAcp = stageCount["at-acp"]
	kpi.CargoAtMalaba = stageCount["at-malaba"]
	kpi.CargoCompleted = stageCount["completed"] + stageCount["demobilised"]

	alerts := make([]dashboardAlert, 0, 16)
	for _, cm := range compliance {
		if cm.Status == "expired" || cm.Status == "missing" {
			alerts = append(alerts, dashboardAlert{
				Severity: "crit",
				Title:    cm.DocType + " " + cm.Status,
				Detail:   complianceSubject(cm, driverByID, vehByID),
				When:     cm.Expiry,
				Href:     "/compliance",
			})
		}
	}
	for _, cm := range compliance {
		if cm.Status == "expiring" {
			alerts = append(alerts, dashboardAlert{
				Severity: "warn",
				Title:    cm.DocType + " expiring",
				Detail:   complianceSubject(cm, driverByID, vehByID) + " · " + cm.Expiry,
				When:     cm.Expiry,
				Href:     "/compliance",
			})
		}
	}
	for _, row := range pmDue {
		sev := "warn"
		if row.DueInKm != nil && *row.DueInKm <= 0 {
			sev = "crit"
		}
		detail := row.Schedule.Name
		if row.Vehicle != nil {
			detail = row.Vehicle.Plate + " · " + detail
		}
		alerts = append(alerts, dashboardAlert{
			Severity: sev,
			Title:    "PM due · " + row.Schedule.ServiceType,
			Detail:   detail,
			When:     row.Schedule.NextDueDate,
			Href:     "/maintenance",
		})
	}
	for _, mx := range maintenance {
		if (mx.Status != "scheduled" && mx.Status != "overdue") || mx.Date == "" || mx.Date >= today {
			continue
		}
		detail := mx.Service
		if v, ok := vehByID[mx.VehicleID]; ok {
			detail = v.Plate + " · " + detail
		}
		alerts = append(alerts, dashboardAlert{
			Severity: "crit",
			Title:    "WO overdue · " + mx.Type,
			Detail:   detail,
			When:     mx.Date,
			Href:     "/maintenance",
		})
	}
	for _, s := range safety {
		if s.Severity == "crit" && (s.Status == "open" || s.Status == "investigating") {
			detail := "—"
			if v, ok := vehByID[s.VehicleID]; ok {
				detail = v.Plate
			}
			alerts = append(alerts, dashboardAlert{
				Severity: "crit",
				Title:    "Safety · " + s.Type,
				Detail:   detail,
				When:     s.Date,
				Href:     "/safety",
			})
		}
	}
	for _, r := range requests {
		if r.Status == "submitted" {
			alerts = append(alerts, dashboardAlert{
				Severity: "warn",
				Title:    "New vehicle request",
				Detail:   r.RequesterDept,
				When:     r.SubmittedAt,
				Href:     "/requests/" + r.ID,
			})
		}
	}
	anomalyCount := 0
	for _, f := range fuel {
		if f.Anomaly == nil || !*f.Anomaly {
			continue
		}
		if anomalyCount >= 3 {
			break
		}
		detail := "?"
		if v, ok := vehByID[f.VehicleID]; ok {
			detail = v.Plate
		}
		if f.AnomalyReason != "" {
			detail += " · " + f.AnomalyReason
		}
		alerts = append(alerts, dashboardAlert{
			Severity: "warn",
			Title:    "Fuel anomaly",
			Detail:   detail,
			When:     f.Date,
			Href:     "/fuel",
		})
		anomalyCount++
	}
	for _, cg := range cargo {
		if cg.Stage == "at-acp" {
			alerts = append(alerts, dashboardAlert{
				Severity: "warn",
				Title:    "Cargo at ACP · awaiting offload",
				Detail:   cg.TruckPlate,
				When:     cg.ArrivalAcp,
				Href:     "/cargo/" + cg.ID,
			})
		}
	}

	// crit first, then warn; within a tier, newest first.
	sort.SliceStable(alerts, func(i, j int) bool {
		if alerts[i].Severity != alerts[j].Severity {
			return alerts[i].Severity == "crit"
		}
		return alerts[i].When > alerts[j].When
	})

	out := dashboardSummary{
		GeneratedAt:   nowISO(),
		Kpis:          kpi,
		CargoPipeline: pipeline,
		Alerts:        alerts,
	}
	blob, err := json.Marshal(out)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if d.Cache != nil && d.TTL > 0 {
		_ = d.Cache.Set(ctx, cache.KeyDashboard, blob, d.TTL)
	}
	c.Data(http.StatusOK, "application/json", blob)
}

func complianceSubject(cm models.ComplianceItem, drivers map[string]models.Driver, vehicles map[string]models.Vehicle) string {
	if cm.DriverID != "" {
		if dr, ok := drivers[cm.DriverID]; ok {
			return dr.Name
		}
		return cm.DriverID
	}
	if cm.VehicleID != "" {
		if v, ok := vehicles[cm.VehicleID]; ok {
			return v.Plate
		}
		return cm.VehicleID
	}
	return "—"
}
