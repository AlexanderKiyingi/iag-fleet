package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/cache"
	"github.com/iag/fleet-tool/backend/internal/models"
)

// Reference exposes static enums and geo data so the frontend can drop the
// hardcoded copies in lib/types.ts and lib/data/geo.ts.
type Reference struct {
	Cache cache.Cache
	TTL   time.Duration
}

func (r *Reference) Register(rg *gin.RouterGroup) {
	rg.GET("/reference",     auth.RequireUser(), r.all)
	rg.GET("/reference/geo", auth.RequireUser(), r.geo)
}

func (r *Reference) all(c *gin.Context) {
	ctx := c.Request.Context()
	if r.Cache != nil && r.TTL > 0 {
		if blob, ok, _ := r.Cache.Get(ctx, cache.KeyReferenceAll); ok && len(blob) > 0 {
			c.Data(http.StatusOK, "application/json", blob)
			return
		}
	}
	payload := gin.H{
		"departments":            models.Departments,
		"safetyTypes":            models.SafetyTypes,
		"complianceDocTypes":     models.ComplianceDocTypes,
		"preferredVehicleTypes":  models.PreferredVehicleTypes,
		"fuelStations":           models.FuelStations,
		"vehicleStatuses":        models.VehicleStatuses,
		"jmpStatuses":            models.JmpStatuses,
		"mileageStatuses":        models.MileageStatuses,
		"requestStatuses":        models.RequestStatuses,
		"taskStates":             models.TaskStates,
		"deploymentMechStatuses": models.DeploymentMechStatuses,
		"deploymentStatuses":     models.DeploymentStatuses,
		"cargoStages":            models.CargoStages,
		"inspectionKinds":        models.InspectionKinds,
		"inspectionStatuses":     models.InspectionStatuses,
		"inspectionItemStatuses": models.InspectionItemStatuses,
		"pmServiceTypes":         models.PMServiceTypes,
		"maintenanceStatuses":    models.MaintenanceStatuses,
		"complianceStatuses":     models.ComplianceStatuses,
	}
	blob, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if r.Cache != nil && r.TTL > 0 {
		_ = r.Cache.Set(ctx, cache.KeyReferenceAll, blob, r.TTL)
	}
	c.Data(http.StatusOK, "application/json", blob)
}

func (r *Reference) geo(c *gin.Context) {
	ctx := c.Request.Context()
	if r.Cache != nil && r.TTL > 0 {
		if blob, ok, _ := r.Cache.Get(ctx, cache.KeyReferenceGeo); ok && len(blob) > 0 {
			c.Data(http.StatusOK, "application/json", blob)
			return
		}
	}
	payload := gin.H{
		"pois":      models.POIs,
		"corridors": models.Corridors,
		"basemaps":  models.Basemaps,
	}
	blob, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if r.Cache != nil && r.TTL > 0 {
		_ = r.Cache.Set(ctx, cache.KeyReferenceGeo, blob, r.TTL)
	}
	c.Data(http.StatusOK, "application/json", blob)
}
