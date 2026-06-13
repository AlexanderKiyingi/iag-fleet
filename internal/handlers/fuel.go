package handlers

import (
	"context"
	"net/http"
	"os"
	"sort"
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
	f := &FuelRecords{
		inner: Resource[models.FuelRecord, *models.FuelRecord]{
			Repo:       repo,
			Collection: repo.Fuel,
			Entity:     "fuel_record",
			IDPrefix:   "FUEL",
		},
		Events: bus,
	}
	f.inner.BeforeCreate = f.beforeWrite
	f.inner.BeforeUpdate = f.beforeWrite
	return f
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
	g.POST("/bulk", add, f.bulkCreate)
	g.PUT("/:id", change, f.inner.replace)
	g.PATCH("/:id", change, f.patch)
	g.PATCH("/bulk", change, f.inner.bulkPatch)
	g.DELETE("/:id", del, f.inner.remove)
	g.DELETE("/bulk", del, f.inner.bulkDelete)

	g.POST("/:id/anomaly-event", change, f.anomalyEvent)
	g.POST("/reconcile", change, f.reconcile)
	// Deprecated alias — same handler as /reconcile.
	g.POST("/link-events", change, f.reconcile)
}

func (f *FuelRecords) beforeWrite(c *gin.Context, rec *models.FuelRecord) error {
	if err := validateFuelRecord(c.Request.Context(), f.inner.Repo, rec, rec.ID); err != nil {
		return err
	}
	return f.enrichFuelRecord(c.Request.Context(), rec, nil)
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
	if err := validateFuelRecord(c.Request.Context(), f.inner.Repo, &item, ""); err != nil {
		respondMutationError(c, err)
		return
	}
	if err := f.enrichFuelRecord(c.Request.Context(), &item, nil); err != nil {
		respondError(c, err)
		return
	}
	created, err := f.inner.Collection.Add(c.Request.Context(), item)
	if err != nil {
		respondError(c, err)
		return
	}
	f.inner.Repo.LogBest(c.Request.Context(), "create", f.inner.Entity, created.ID, "", currentUser(c, f.inner.Repo))
	f.publishFuel(c, created)
	if _, err := jobs.ReconcileFuel(c.Request.Context(), f.inner.Repo.FuelDB(), 30, created.VehicleID); err == nil {
		if refreshed, err := f.inner.Collection.Get(c.Request.Context(), created.ID); err == nil {
			created = refreshed
		}
	}
	c.JSON(http.StatusCreated, created)
}

func (f *FuelRecords) bulkCreate(c *gin.Context) {
	var items []models.FuelRecord
	if err := c.ShouldBindJSON(&items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(items) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty batch"})
		return
	}
	if len(items) > maxBulkItems {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error":  "batch too large",
			"limit":  maxBulkItems,
			"actual": len(items),
		})
		return
	}
	for i := range items {
		if items[i].ID == "" {
			items[i].ID = generateID(f.inner.IDPrefix)
		}
	}

	ctx := c.Request.Context()
	existingByVeh, err := f.loadFuelHistoryByVehicle(ctx, items)
	if err != nil {
		respondError(c, err)
		return
	}

	byVeh := make(map[string][]*models.FuelRecord)
	for i := range items {
		byVeh[items[i].VehicleID] = append(byVeh[items[i].VehicleID], &items[i])
	}
	for _, batch := range byVeh {
		sort.SliceStable(batch, func(i, j int) bool {
			if batch[i].Date != batch[j].Date {
				return batch[i].Date < batch[j].Date
			}
			return batch[i].ID < batch[j].ID
		})
		accum := append([]models.FuelRecord(nil), existingByVeh[batch[0].VehicleID]...)
		for _, rec := range batch {
			if err := validateFuelValues(rec); err != nil {
				respondMutationError(c, err)
				return
			}
			accum = fueldetect.UpsertHistoryRecord(accum, rec)
			if err := f.enrichFuelRecord(ctx, rec, accum); err != nil {
				respondError(c, err)
				return
			}
		}
	}

	created, err := f.inner.Collection.BulkAdd(ctx, items)
	if err != nil {
		respondError(c, err)
		return
	}
	user := currentUser(c, f.inner.Repo)
	for i := range created {
		f.inner.Repo.LogBest(ctx, "create", f.inner.Entity, created[i].ID, "bulk", user)
	}
	c.JSON(http.StatusCreated, gin.H{"added": len(created), "items": created})
}

func (f *FuelRecords) loadFuelHistoryByVehicle(ctx context.Context, items []models.FuelRecord) (map[string][]models.FuelRecord, error) {
	vehIDs := make(map[string]struct{})
	for _, it := range items {
		if it.VehicleID != "" {
			vehIDs[it.VehicleID] = struct{}{}
		}
	}
	out := make(map[string][]models.FuelRecord, len(vehIDs))
	for vid := range vehIDs {
		rows, _, err := f.inner.Repo.Fuel.ListFiltered(ctx, store.ListFilter{
			Filters:  map[string]string{"vehicle_id": vid},
			Limit:    500,
			OrderBy:  "date",
			OrderAsc: true,
		})
		if err != nil {
			return nil, err
		}
		out[vid] = rows
	}
	return out, nil
}

func (f *FuelRecords) reconcile(c *gin.Context) {
	lookback, _ := strconv.Atoi(c.DefaultQuery("lookbackDays", "90"))
	vehicleID := c.Query("vehicleId")
	result, err := jobs.ReconcileFuel(c.Request.Context(), f.inner.Repo.FuelDB(), lookback, vehicleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
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
	if err := validateFuelRecord(ctx, f.inner.Repo, &merged, id); err != nil {
		respondMutationError(c, err)
		return
	}
	if err := f.enrichFuelRecord(ctx, &merged, nil); err != nil {
		respondError(c, err)
		return
	}
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

// enrichFuelRecord runs anomaly detection. batchHistory, when non-nil, is used
// instead of loading from the DB (bulk import within one vehicle batch).
func (f *FuelRecords) enrichFuelRecord(ctx context.Context, rec *models.FuelRecord, batchHistory []models.FuelRecord) error {
	hadAnomaly := rec.Anomaly != nil && *rec.Anomaly
	var actx fueldetect.AnomalyContext
	var err error
	if batchHistory != nil {
		tank, tracked := 0, false
		if veh, e := f.inner.Repo.Vehicles.Get(ctx, rec.VehicleID); e == nil {
			if veh.TankCapacityLitres != nil {
				tank = *veh.TankCapacityLitres
			}
			tracked = veh.FuelTracker
		}
		telL := 0.0
		if rec.FuelEventID != nil && *rec.FuelEventID > 0 {
			telL = fueldetect.LoadTelemetryLitres(ctx, f.inner.Repo, *rec.FuelEventID)
		}
		actx = fueldetect.BuildAnomalyContextFromHistory(rec, batchHistory, tank, tracked, telL)
	} else {
		actx, err = fueldetect.BuildAnomalyContext(ctx, f.inner.Repo, rec)
		if err != nil {
			return err
		}
	}
	fueldetect.ApplyEnrichment(rec, actx)
	if rec.Anomaly != nil && *rec.Anomaly && !hadAnomaly {
		if len(rec.AnomalyHistory) == 0 {
			appendAnomalyEvent(&rec.AnomalyHistory, "flagged", "", rec.AnomalyReason)
		}
		rec.AnomalyStatus = "open"
	}
	return nil
}

type fuelAnomalyEventBody struct {
	Event string `json:"event" binding:"required"` // investigate | confirm | resolve | dismiss | reopen
	Note  string `json:"note,omitempty"`
}

func (f *FuelRecords) anomalyEvent(c *gin.Context) {
	var body fuelAnomalyEventBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	switch body.Event {
	case "investigate", "confirm", "resolve", "dismiss", "reopen":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "event must be investigate, confirm, resolve, dismiss, or reopen"})
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
		case "confirm":
			// Confirmed real fuel loss — keep the anomaly flagged, record the
			// confirmation in the lifecycle history.
			rec.AnomalyStatus = "confirmed"
			appendAnomalyEvent(&rec.AnomalyHistory, "confirmed", user, body.Note)
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
