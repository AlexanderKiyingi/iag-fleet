package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
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
	VehiclesTotal      int     `json:"vehiclesTotal"`
	Moving             int     `json:"moving"`
	UtilPct            int     `json:"utilPct"`
	OfflineOrMx        int     `json:"offlineOrMaintenance"`
	JmpsActive         int     `json:"jmpsActive"`
	JmpsPending        int     `json:"jmpsPendingToolbox"`
	RequestsOpen       int     `json:"requestsOpen"`
	CargoAtAcp         int     `json:"cargoAtAcp"`
	CargoAtMalaba      int     `json:"cargoAtMalaba"`
	CargoCompleted     int     `json:"cargoCompleted"`
	FuelMtdUgx         float64 `json:"fuelMtdUgx"`
	FuelAnomalies      int     `json:"fuelAnomalies"`
	ComplianceAtRisk   int     `json:"complianceAtRisk"`
	PmDue              int     `json:"pmDue"`
	MaintenanceOverdue int     `json:"maintenanceOverdue"`
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
	// AlertsTruncated names the alert sources that hit their per-source cap.
	// Empty means the feed is complete. Surfaced rather than hidden so the UI
	// can say "showing the most recent 50" instead of implying that is all.
	AlertsTruncated []string `json:"alertsTruncated,omitempty"`
}

func (d *Dashboard) summary(c *gin.Context) {
	ctx := c.Request.Context()

	if d.Cache != nil && d.TTL > 0 {
		if blob, ok, _ := d.Cache.Get(ctx, cache.KeyDashboard); ok && len(blob) > 0 {
			c.Data(http.StatusOK, "application/json", blob)
			return
		}
	}

	// RecomputeComplianceStatuses used to run right here, on this GET. It walks
	// every compliance row and issues a separate write transaction for each one
	// whose status has drifted past its expiry date, so the first person to open
	// the dashboard after midnight paid for every row that rolled over — and no
	// dashboard request could ever be served from a read replica. It is a
	// maintenance task on a clock, not a read, so it lives in the job runner
	// now (see internal/jobs). The statuses it maintains are read below.

	counters, err := d.Repo.DashboardCounters(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stageCount, err := d.Repo.CargoStageCounts(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	kpi := dashboardKpis{
		VehiclesTotal:      counters.VehiclesTotal,
		Moving:             counters.Moving,
		OfflineOrMx:        counters.OfflineOrMx,
		JmpsActive:         counters.JmpsActive,
		JmpsPending:        counters.JmpsPending,
		RequestsOpen:       counters.RequestsOpen,
		FuelMtdUgx:         counters.FuelMtdUgx,
		FuelAnomalies:      counters.FuelAnomalies,
		ComplianceAtRisk:   counters.ComplianceAtRisk,
		MaintenanceOverdue: counters.MaintenanceOverdue,
	}
	if kpi.VehiclesTotal > 0 {
		kpi.UtilPct = int((float64(kpi.Moving) / float64(kpi.VehiclesTotal)) * 100.0)
	}
	kpi.CargoAtAcp = stageCount["at-acp"]
	kpi.CargoAtMalaba = stageCount["at-malaba"]
	kpi.CargoCompleted = stageCount["completed"] + stageCount["demobilised"]

	pmDue, err := d.Repo.ListDuePMSchedules(ctx, jobs.DefaultPMWithinDays, jobs.DefaultPMWithinKm)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	kpi.PmDue = len(pmDue)

	pipeline := make([]cargoPipelineNode, 0, len(models.CargoStages))
	for _, s := range models.CargoStages {
		pipeline = append(pipeline, cargoPipelineNode{Stage: s.K, Label: s.Label, Count: stageCount[s.K]})
	}

	rows, truncated, err := d.Repo.DashboardAlerts(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	alerts := make([]dashboardAlert, 0, len(rows)+len(pmDue))
	for _, a := range rows {
		alerts = append(alerts, dashboardAlert{
			Severity: a.Severity, Title: a.Title, Detail: a.Detail,
			When: a.When, Href: a.Href,
		})
	}
	// PM schedules stay in Go: ListDuePMSchedules already resolves them against
	// both a date and an odometer threshold, and re-expressing that in SQL here
	// would fork the rule that the PM job also depends on.
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

	// crit first, then warn; within a tier, newest first.
	sort.SliceStable(alerts, func(i, j int) bool {
		if alerts[i].Severity != alerts[j].Severity {
			return alerts[i].Severity == "crit"
		}
		return alerts[i].When > alerts[j].When
	})

	out := dashboardSummary{
		GeneratedAt:     nowISO(),
		Kpis:            kpi,
		CargoPipeline:   pipeline,
		Alerts:          alerts,
		AlertsTruncated: truncated,
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

// complianceSubject is gone with the in-Go alert assembly it existed for: the
// display name now comes from the LEFT JOINs in Repository.DashboardAlerts,
// rather than from loading the whole driver and vehicle tables to build lookup
// maps. The notifications producer has its own complianceSubjectNamed for the
// case where it already holds the name and plate.
