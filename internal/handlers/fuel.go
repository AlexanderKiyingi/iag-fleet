package handlers

import (
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/events"
	fueldetect "github.com/iag/fleet-tool/backend/internal/fuel"
	"github.com/iag/fleet-tool/backend/internal/jobs"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// FuelRecords is CRUD for fuel_records plus finance event publishing to iag.finance.
type FuelRecords struct {
	inner  Resource[models.FuelRecord, *models.FuelRecord]
	Events *events.Bus
}

func NewFuelRecords(repo *store.Repository, bus *events.Bus) *FuelRecords {
	return &FuelRecords{
		inner: Resource[models.FuelRecord, *models.FuelRecord]{
			Repo:       repo,
			Collection: repo.Fuel,
			Entity:     "fuel_record",
			IDPrefix:   "FUEL",
		},
		Events: bus,
	}
}

func (f *FuelRecords) Register(rg *gin.RouterGroup, base string) {
	g := rg.Group(base)
	view := auth.RequirePerm("view_" + f.inner.Entity)
	add := auth.RequirePerm("add_" + f.inner.Entity)
	change := auth.RequirePerm("change_" + f.inner.Entity)
	del := auth.RequirePerm("delete_" + f.inner.Entity)

	g.GET("", view, f.inner.list)
	g.GET("/search", view, f.inner.search)
	g.GET("/:id", view, f.inner.get)
	g.POST("", add, f.create)
	g.POST("/bulk", add, f.inner.bulkCreate)
	g.PUT("/:id", change, f.inner.replace)
	g.PATCH("/:id", change, f.patch)
	g.PATCH("/bulk", change, f.inner.bulkPatch)
	g.DELETE("/:id", del, f.inner.remove)
	g.DELETE("/bulk", del, f.inner.bulkDelete)

	g.POST("/:id/anomaly-event", change, f.anomalyEvent)
	g.POST("/link-events", change, f.linkEvents)
}

func (f *FuelRecords) create(c *gin.Context) {
	var item models.FuelRecord
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if item.ID == "" {
		item.ID = generateID(f.inner.IDPrefix)
	}
	normalizeFuelRecord(&item)
	created, err := f.inner.Collection.Add(c.Request.Context(), item)
	if err != nil {
		respondError(c, err)
		return
	}
	f.inner.Repo.LogBest(c.Request.Context(), "create", f.inner.Entity, created.ID, "", currentUser(c, f.inner.Repo))
	f.publishFuel(c, created)
	if n, err := jobs.LinkFuelEvents(c.Request.Context(), f.inner.Repo.Pool(), 30); err == nil && n > 0 {
		if refreshed, err := f.inner.Collection.Get(c.Request.Context(), created.ID); err == nil {
			created = refreshed
		}
	}
	c.JSON(http.StatusCreated, created)
}

func (f *FuelRecords) linkEvents(c *gin.Context) {
	lookback, _ := strconv.Atoi(c.DefaultQuery("lookbackDays", "90"))
	linked, err := jobs.LinkFuelEvents(c.Request.Context(), f.inner.Repo.Pool(), lookback)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"linked": linked})
}

func (f *FuelRecords) patch(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	existing, err := f.inner.Collection.Get(ctx, id)
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
	normalizeFuelRecord(&merged)
	updated, err := f.inner.Collection.Replace(ctx, id, merged)
	if err != nil {
		respondError(c, err)
		return
	}
	f.inner.Repo.LogBest(ctx, "update", f.inner.Entity, id, "", currentUser(c, f.inner.Repo))
	f.publishFuel(c, updated)
	c.JSON(http.StatusOK, updated)
}

func (f *FuelRecords) publishFuel(c *gin.Context, rec models.FuelRecord) {
	if f.Events == nil || !f.Events.Enabled() || rec.Total <= 0 {
		return
	}
	currency := envOr("FLEET_FUEL_CURRENCY", "UGX")
	vendor := rec.Station
	if vendor == "" {
		vendor = "fleet-fuel"
	}
	f.Events.PublishFuelRecorded(c.Request.Context(), rec.ID, rec.Total, currency, vendor, rec.VehicleID, rec.Litres)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func normalizeFuelRecord(rec *models.FuelRecord) {
	hadAnomaly := rec.Anomaly != nil && *rec.Anomaly
	fueldetect.EnrichAnomaly(rec)
	if rec.Anomaly != nil && *rec.Anomaly && !hadAnomaly {
		if len(rec.AnomalyHistory) == 0 {
			appendAnomalyEvent(&rec.AnomalyHistory, "flagged", "", rec.AnomalyReason)
		}
		if rec.AnomalyStatus == "" {
			rec.AnomalyStatus = "open"
		}
	}
}

type fuelAnomalyEventBody struct {
	Event string `json:"event" binding:"required"` // investigate | resolve | dismiss | reopen
	Note  string `json:"note,omitempty"`
}

func (f *FuelRecords) anomalyEvent(c *gin.Context) {
	var body fuelAnomalyEventBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	switch body.Event {
	case "investigate", "resolve", "dismiss", "reopen":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "event must be investigate, resolve, dismiss, or reopen"})
		return
	}
	id := c.Param("id")
	ctx := c.Request.Context()
	user := currentUser(c, f.inner.Repo)

	existing, err := f.inner.Collection.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if existing.Anomaly == nil || !*existing.Anomaly {
		if body.Event != "reopen" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "record has no active anomaly"})
			return
		}
	}

	updated, err := f.inner.Collection.Update(ctx, id, func(rec *models.FuelRecord) {
		switch body.Event {
		case "investigate":
			rec.AnomalyStatus = "investigating"
			appendAnomalyEvent(&rec.AnomalyHistory, "investigated", user, body.Note)
		case "resolve":
			rec.AnomalyStatus = "resolved"
			appendAnomalyEvent(&rec.AnomalyHistory, "resolved", user, body.Note)
		case "dismiss":
			rec.AnomalyStatus = "dismissed"
			f := false
			rec.Anomaly = &f
			appendAnomalyEvent(&rec.AnomalyHistory, "dismissed", user, body.Note)
		case "reopen":
			t := true
			rec.Anomaly = &t
			rec.AnomalyStatus = "open"
			appendAnomalyEvent(&rec.AnomalyHistory, "flagged", user, body.Note)
		}
	})
	if err != nil {
		respondError(c, err)
		return
	}
	f.inner.Repo.LogBest(ctx, "anomaly:"+body.Event, f.inner.Entity, id, body.Note, user)
	c.JSON(http.StatusOK, updated)
}
