package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/jobs"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// PMSchedules manages preventive maintenance schedules (Fleetio-style PM engine).
type PMSchedules struct {
	Repo   *store.Repository
	Events *events.Bus
}

func (h *PMSchedules) Register(rg *gin.RouterGroup) {
	// PM's domain logic (validate + next-due recompute) lives in the
	// BeforeCreate/BeforeUpdate hooks so the generic CRUD — including the
	// /bulk endpoints — applies it uniformly. This is what lets PM share
	// the same bulk-create/patch/delete surface as every other entity
	// instead of hand-rolled per-route handlers.
	res := Resource[models.PMSchedule, *models.PMSchedule]{
		Repo: h.Repo, Collection: h.Repo.PMSchedules,
		Entity: "pm_schedule", IDPrefix: "PM",
		BeforeCreate: h.validateAndRecompute,
		BeforeUpdate: h.validateAndRecompute,
	}
	g := rg.Group("/pm-schedules")
	view := auth.RequirePerm("view_pm_schedule")
	add := auth.RequirePerm("add_pm_schedule")
	change := auth.RequirePerm("change_pm_schedule")
	del := auth.RequirePerm("delete_pm_schedule")

	g.GET("", view, res.list)
	g.GET("/search", view, res.search)
	g.GET("/due", view, h.due)
	g.GET("/:id", view, res.get)
	g.POST("", add, res.create)
	g.POST("/bulk", add, res.bulkCreate)
	g.POST("/evaluate", change, h.evaluate)
	g.PUT("/:id", change, res.replace)
	g.PATCH("/:id", change, res.patch)
	g.PATCH("/bulk", change, res.bulkPatch)
	g.DELETE("/:id", del, res.remove)
	g.DELETE("/bulk", del, res.bulkDelete)
}

// validateAndRecompute is the shared create/update hook. It enforces the PM
// domain invariants (name, service type, positive intervals, vehicle exists)
// and then derives next_due_km / next_due_date from the intervals and the
// last-service markers. The two hook fields share one signature, so the same
// method backs both BeforeCreate and BeforeUpdate — and, through them, the
// generic single-row and bulk handlers alike. It reads only the item's own
// fields (no path params), which is what makes it safe to run per row in a
// batch.
func (h *PMSchedules) validateAndRecompute(c *gin.Context, item *models.PMSchedule) error {
	if err := validatePMSchedule(c.Request.Context(), h.Repo, item); err != nil {
		return err
	}
	store.RecomputePMNextDue(item)
	return nil
}

func pmThresholds(c *gin.Context, strict bool) (withinDays int, withinKm float64) {
	withinDays, _ = strconv.Atoi(c.DefaultQuery("withinDays", ""))
	withinKm, _ = strconv.ParseFloat(c.DefaultQuery("withinKm", ""), 64)
	if strict {
		if c.Query("withinDays") == "" {
			withinDays = 0
		}
		if c.Query("withinKm") == "" {
			withinKm = 0
		}
		return withinDays, withinKm
	}
	if withinDays <= 0 {
		withinDays = jobs.DefaultPMWithinDays
	}
	if withinKm <= 0 {
		withinKm = jobs.DefaultPMWithinKm
	}
	return withinDays, withinKm
}

func (h *PMSchedules) due(c *gin.Context) {
	withinDays, withinKm := pmThresholds(c, false)
	rows, err := h.Repo.ListDuePMSchedules(c.Request.Context(), withinDays, withinKm)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "count": len(rows)})
}

func (h *PMSchedules) evaluate(c *gin.Context) {
	withinDays, withinKm := pmThresholds(c, c.Query("strict") == "true" || c.Query("strict") == "1")
	ctx := c.Request.Context()
	res, err := h.Repo.EvaluatePMSchedules(ctx, withinDays, withinKm)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	user := currentUser(c, h.Repo)
	for _, id := range res.CreatedIDs {
		h.Repo.LogBest(ctx, "auto-create", "maintenance_item", id, "pm-evaluate", user)
		if h.Events != nil && h.Events.Enabled() {
			h.Events.PublishFleet(ctx, events.TypeMaintenanceCreated, events.FleetEventData(map[string]string{
				"maintenanceId": id,
				"source":        "pm_evaluate",
			}), id, "")
		}
	}
	c.JSON(http.StatusOK, res)
}
