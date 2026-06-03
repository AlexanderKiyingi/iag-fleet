package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/alvor-technologies/iag-platform-go/apierr"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/cache"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Admin owns the non-CRUD endpoints: ticker, audit, export/import/reset.
type Admin struct {
	Repo  *store.Repository
	Cache cache.Cache
}

func (a *Admin) Register(rg *gin.RouterGroup) {
	rg.GET("/ticker", auth.RequirePerm("view_operator_ticker"), a.getTicker)
	rg.PATCH("/ticker", auth.RequirePerm("change_operator_ticker"), a.patchTicker)

	rg.GET("/audit", auth.RequirePerm("view_audit_entry"), a.listAudit)
	rg.GET("/audit/search", auth.RequirePerm("view_audit_entry"), a.searchAudit)
	rg.GET("/admin/audit-logs", auth.RequirePerm("view_audit_entry"), a.listAPIAuditLogs)
	// POST /audit was deliberately removed: the audit log is append-only-
	// by-system. Every domain action that should be auditable goes through
	// Repository.LogBest from the relevant handler. Letting clients write
	// arbitrary rows (especially under a read-only permission) defeats
	// tamper-evidence. If a future use case needs operator-authored entries,
	// add a dedicated note/comment table — don't reopen this endpoint.

	rg.GET("/admin/export", auth.RequirePerm("export_data"), a.export)
	rg.POST("/admin/import", auth.RequirePerm("import_data"), a.importAll)
	rg.POST("/admin/reset", auth.RequirePerm("reset_data"), a.reset)
}

func (a *Admin) getTicker(c *gin.Context) {
	t, err := a.Repo.Ticker(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, t)
}

func (a *Admin) patchTicker(c *gin.Context) {
	var patch models.OperatorTicker
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	t, err := a.Repo.PatchTicker(c.Request.Context(), patch)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, t)
}

func (a *Admin) listAudit(c *gin.Context) {
	entries, err := a.Repo.Audit(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, entries)
}

// searchAudit is the filterable + paginated variant of /api/audit. All
// filters are query params; unknown params are ignored. Time inputs accept
// RFC3339 ("2026-05-04T00:00:00Z") or a bare date ("2026-05-04", treated
// as midnight UTC). Returns:
//
//	{
//	  "items":  [...AuditEntry],
//	  "total":  <int>,                  // total matching the filter
//	  "limit":  <int>,                  // echoed back, possibly clamped
//	  "offset": <int>,
//	}
func (a *Admin) searchAudit(c *gin.Context) {
	f := store.AuditFilter{
		Entity: c.Query("entity"),
		RefID:  c.Query("refId"),
		User:   c.Query("user"),
		Action: c.Query("action"),
		Q:      c.Query("q"),
	}
	if v := c.Query("from"); v != "" {
		t, err := parseAuditTime(v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'from': " + err.Error()})
			return
		}
		f.From = &t
	}
	if v := c.Query("to"); v != "" {
		t, err := parseAuditTime(v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'to': " + err.Error()})
			return
		}
		f.To = &t
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Offset = n
		}
	}

	items, total, err := a.Repo.AuditSearch(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []models.AuditEntry{}
	}
	// Echo the effective limit/offset (the repo may have clamped) so the
	// UI can render pagination controls without re-deriving the bounds.
	effLimit := f.Limit
	if effLimit <= 0 {
		effLimit = 50
	}
	if effLimit > 500 {
		effLimit = 500
	}
	c.JSON(http.StatusOK, gin.H{
		"items":  items,
		"total":  total,
		"limit":  effLimit,
		"offset": f.Offset,
	})
}

func (a *Admin) listAPIAuditLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, total, err := a.Repo.ListAPIAuditLogs(c.Request.Context(), limit)
	if err != nil {
		apierr.JSON(c, http.StatusInternalServerError, apierr.CodeInternal, "could not list API audit logs")
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

// parseAuditTime accepts RFC3339 or bare YYYY-MM-DD (interpreted as UTC
// midnight). Anything else is rejected — we don't want to silently
// misinterpret an operator's intent on a date filter.
func parseAuditTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}

// Snapshot is the shape used by export/import — same keys as the frontend
// state so an existing localStorage dump can be loaded directly.
type Snapshot struct {
	V          int                      `json:"v"`
	ExportedAt string                   `json:"exportedAt,omitempty"`
	Vehicles   []models.Vehicle         `json:"vehicles,omitempty"`
	Drivers    []models.Driver          `json:"drivers,omitempty"`
	JMPs       []models.JMP             `json:"jmps,omitempty"`
	Cargo      []models.Cargo           `json:"cargo,omitempty"`
	CargoDocs  []models.CargoDoc        `json:"cargoDocs,omitempty"`
	Fuel       []models.FuelRecord      `json:"fuel,omitempty"`
	Maint      []models.MaintenanceItem `json:"maintenance,omitempty"`
	Parts      []models.Part            `json:"parts,omitempty"`
	Tyres      []models.Tyre            `json:"tyres,omitempty"`
	Trips      []models.Trip            `json:"trips,omitempty"`
	Safety     []models.SafetyEvent     `json:"safety,omitempty"`
	Compliance []models.ComplianceItem  `json:"compliance,omitempty"`
	Requests   []models.ServiceRequest  `json:"requests,omitempty"`
	Tasks      []models.TaskItem        `json:"tasks,omitempty"`
	Deployment          []models.DeploymentDay        `json:"deployment,omitempty"`
	Audit               []models.AuditEntry           `json:"audit,omitempty"`
	Ticker              *models.OperatorTicker        `json:"ticker,omitempty"`
	InspectionTemplates []models.InspectionTemplate   `json:"inspectionTemplates,omitempty"`
	Inspections         []models.VehicleInspection    `json:"inspections,omitempty"`
	PMSchedules         []models.PMSchedule             `json:"pmSchedules,omitempty"`
}

func (a *Admin) export(c *gin.Context) {
	ctx := c.Request.Context()
	snap := Snapshot{
		V:          7,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
	}
	type listFn func(context.Context) error
	lists := []listFn{
		func(ctx context.Context) (err error) { snap.Vehicles, err = a.Repo.Vehicles.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Drivers, err = a.Repo.Drivers.List(ctx); return },
		func(ctx context.Context) (err error) { snap.JMPs, err = a.Repo.JMPs.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Cargo, err = a.Repo.Cargo.List(ctx); return },
		func(ctx context.Context) (err error) { snap.CargoDocs, err = a.Repo.CargoDocs.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Fuel, err = a.Repo.Fuel.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Maint, err = a.Repo.Maintenance.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Parts, err = a.Repo.Parts.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Tyres, err = a.Repo.Tyres.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Trips, err = a.Repo.Trips.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Safety, err = a.Repo.Safety.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Compliance, err = a.Repo.Compliance.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Requests, err = a.Repo.Requests.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Tasks, err = a.Repo.Tasks.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Deployment, err = a.Repo.Deployment.List(ctx); return },
		func(ctx context.Context) (err error) { snap.Audit, err = a.Repo.Audit(ctx); return },
		func(ctx context.Context) (err error) {
			snap.InspectionTemplates, err = a.Repo.InspectionTemplates.List(ctx)
			return
		},
		func(ctx context.Context) (err error) { snap.Inspections, err = a.Repo.Inspections.List(ctx); return },
		func(ctx context.Context) (err error) { snap.PMSchedules, err = a.Repo.PMSchedules.List(ctx); return },
	}
	for _, fn := range lists {
		if err := fn(ctx); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if t, err := a.Repo.Ticker(ctx); err == nil {
		snap.Ticker = &t
	}
	c.JSON(http.StatusOK, snap)
}

func (a *Admin) importAll(c *gin.Context) {
	var snap Snapshot
	if err := c.ShouldBindJSON(&snap); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	type setFn func() error
	steps := []setFn{
		func() error {
			if snap.Vehicles == nil {
				return nil
			}
			return a.Repo.Vehicles.SetAll(ctx, snap.Vehicles)
		},
		func() error { if snap.Drivers == nil { return nil }; return a.Repo.Drivers.SetAll(ctx, snap.Drivers) },
		func() error { if snap.JMPs == nil { return nil }; return a.Repo.JMPs.SetAll(ctx, snap.JMPs) },
		func() error { if snap.Cargo == nil { return nil }; return a.Repo.Cargo.SetAll(ctx, snap.Cargo) },
		func() error { if snap.CargoDocs == nil { return nil }; return a.Repo.CargoDocs.SetAll(ctx, snap.CargoDocs) },
		func() error { if snap.Fuel == nil { return nil }; return a.Repo.Fuel.SetAll(ctx, snap.Fuel) },
		func() error { if snap.Maint == nil { return nil }; return a.Repo.Maintenance.SetAll(ctx, snap.Maint) },
		func() error { if snap.Parts == nil { return nil }; return a.Repo.Parts.SetAll(ctx, snap.Parts) },
		func() error { if snap.Tyres == nil { return nil }; return a.Repo.Tyres.SetAll(ctx, snap.Tyres) },
		func() error { if snap.Trips == nil { return nil }; return a.Repo.Trips.SetAll(ctx, snap.Trips) },
		func() error { if snap.Safety == nil { return nil }; return a.Repo.Safety.SetAll(ctx, snap.Safety) },
		func() error { if snap.Compliance == nil { return nil }; return a.Repo.Compliance.SetAll(ctx, snap.Compliance) },
		func() error { if snap.Requests == nil { return nil }; return a.Repo.Requests.SetAll(ctx, snap.Requests) },
		func() error { if snap.Tasks == nil { return nil }; return a.Repo.Tasks.SetAll(ctx, snap.Tasks) },
		func() error { if snap.Deployment == nil { return nil }; return a.Repo.Deployment.SetAll(ctx, snap.Deployment) },
		func() error {
			if snap.InspectionTemplates == nil {
				return nil
			}
			return a.Repo.InspectionTemplates.SetAll(ctx, snap.InspectionTemplates)
		},
		func() error {
			if snap.Inspections == nil {
				return nil
			}
			return a.Repo.Inspections.SetAll(ctx, snap.Inspections)
		},
		func() error {
			if snap.PMSchedules == nil {
				return nil
			}
			return a.Repo.PMSchedules.SetAll(ctx, snap.PMSchedules)
		},
	}
	for _, fn := range steps {
		if err := fn(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if snap.Ticker != nil {
		if _, err := a.Repo.PatchTicker(ctx, *snap.Ticker); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	_ = cache.InvalidateFleetAggregates(ctx, a.Cache)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *Admin) reset(c *gin.Context) {
	ctx := c.Request.Context()
	type clearFn func() error
	steps := []clearFn{
		func() error { return a.Repo.Vehicles.SetAll(ctx, nil) },
		func() error { return a.Repo.Drivers.SetAll(ctx, nil) },
		func() error { return a.Repo.JMPs.SetAll(ctx, nil) },
		func() error { return a.Repo.Cargo.SetAll(ctx, nil) },
		func() error { return a.Repo.CargoDocs.SetAll(ctx, nil) },
		func() error { return a.Repo.Fuel.SetAll(ctx, nil) },
		func() error { return a.Repo.Maintenance.SetAll(ctx, nil) },
		func() error { return a.Repo.Parts.SetAll(ctx, nil) },
		func() error { return a.Repo.Tyres.SetAll(ctx, nil) },
		func() error { return a.Repo.Trips.SetAll(ctx, nil) },
		func() error { return a.Repo.Safety.SetAll(ctx, nil) },
		func() error { return a.Repo.Compliance.SetAll(ctx, nil) },
		func() error { return a.Repo.Requests.SetAll(ctx, nil) },
		func() error { return a.Repo.Tasks.SetAll(ctx, nil) },
		func() error { return a.Repo.Deployment.SetAll(ctx, nil) },
		func() error { return a.Repo.InspectionTemplates.SetAll(ctx, nil) },
		func() error { return a.Repo.Inspections.SetAll(ctx, nil) },
		func() error { return a.Repo.PMSchedules.SetAll(ctx, nil) },
	}
	for _, fn := range steps {
		if err := fn(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	_ = cache.InvalidateFleetAggregates(ctx, a.Cache)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
