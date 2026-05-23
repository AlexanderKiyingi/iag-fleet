package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// PMSchedules manages preventive maintenance schedules (Fleetio-style PM engine).
type PMSchedules struct {
	Repo *store.Repository
}

func (h *PMSchedules) Register(rg *gin.RouterGroup) {
	res := Resource[models.PMSchedule, *models.PMSchedule]{
		Repo: h.Repo, Collection: h.Repo.PMSchedules,
		Entity: "pm_schedule", IDPrefix: "PM",
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
	g.POST("", add, h.create)
	g.POST("/evaluate", change, h.evaluate)
	g.PUT("/:id", change, h.replace)
	g.PATCH("/:id", change, h.patch)
	g.DELETE("/:id", del, res.remove)
}

func (h *PMSchedules) due(c *gin.Context) {
	withinDays, _ := strconv.Atoi(c.DefaultQuery("withinDays", "14"))
	withinKm, _ := strconv.ParseFloat(c.DefaultQuery("withinKm", "500"), 64)
	if withinDays <= 0 {
		withinDays = 14
	}
	if withinKm <= 0 {
		withinKm = 500
	}
	rows, err := h.Repo.ListDuePMSchedules(c.Request.Context(), withinDays, withinKm)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "count": len(rows)})
}

func (h *PMSchedules) evaluate(c *gin.Context) {
	withinDays, _ := strconv.Atoi(c.DefaultQuery("withinDays", "0"))
	withinKm, _ := strconv.ParseFloat(c.DefaultQuery("withinKm", "0"), 64)
	if withinDays <= 0 {
		withinDays = 0
	}
	if withinKm <= 0 {
		withinKm = 0
	}
	res, err := h.Repo.EvaluatePMSchedules(c.Request.Context(), withinDays, withinKm)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *PMSchedules) create(c *gin.Context) {
	var item models.PMSchedule
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if item.ID == "" {
		item.ID = generateID("PM")
	}
	store.RecomputePMNextDue(&item)
	created, err := h.Repo.PMSchedules.Add(c.Request.Context(), item)
	if err != nil {
		respondError(c, err)
		return
	}
	h.Repo.LogBest(c.Request.Context(), "create", "pm_schedule", created.ID, "", currentUser(c, h.Repo))
	c.JSON(http.StatusCreated, created)
}

func (h *PMSchedules) replace(c *gin.Context) {
	id := c.Param("id")
	var item models.PMSchedule
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item.ID = id
	store.RecomputePMNextDue(&item)
	updated, err := h.Repo.PMSchedules.Replace(c.Request.Context(), id, item)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *PMSchedules) patch(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	existing, err := h.Repo.PMSchedules.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	patchBytes, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	merged, err := mergeJSON(existing, patchBytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	store.RecomputePMNextDue(&merged)
	updated, err := h.Repo.PMSchedules.Replace(ctx, id, merged)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}
