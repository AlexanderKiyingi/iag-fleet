package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Inspections exposes DVIR-style inspection templates and vehicle inspections.
type Inspections struct {
	Repo *store.Repository
}

func (h *Inspections) Register(rg *gin.RouterGroup) {
	tpl := Resource[models.InspectionTemplate, *models.InspectionTemplate]{
		Repo: h.Repo, Collection: h.Repo.InspectionTemplates,
		Entity: "inspection_template", IDPrefix: "TPL",
	}
	tpl.Register(rg, "/inspection-templates")

	ins := Resource[models.VehicleInspection, *models.VehicleInspection]{
		Repo: h.Repo, Collection: h.Repo.Inspections,
		Entity: "vehicle_inspection", IDPrefix: "INS",
	}
	ins.Register(rg, "/inspections")

	rg.POST("/inspections/:id/submit", auth.RequirePerm("change_vehicle_inspection"), h.submit)
	rg.POST("/inspections/:id/create-defect-wo", auth.RequirePerm("change_vehicle_inspection"), h.createDefectWO)
}

type submitInspectionBody struct {
	Signature string `json:"signature"`
}

func (h *Inspections) submit(c *gin.Context) {
	id := c.Param("id")
	var body submitInspectionBody
	_ = c.ShouldBindJSON(&body)

	ctx := c.Request.Context()
	updated, err := h.Repo.Inspections.Update(ctx, id, func(ins *models.VehicleInspection) {
		tpl, err := h.Repo.InspectionTemplates.Get(ctx, ins.TemplateID)
		if err != nil {
			return
		}
		required := map[string]bool{}
		for _, item := range tpl.Checklist {
			if item.Required {
				required[item.ID] = true
			}
		}
		answered := map[string]string{}
		for _, r := range ins.Results {
			answered[r.ItemID] = r.Status
		}
		failed := false
		var defects models.InspectionDefects
		for itemID := range required {
			st, ok := answered[itemID]
			if !ok || st == "" {
				failed = true
				defects = append(defects, models.InspectionDefect{
					ItemID:      itemID,
					Description: "Required item not checked",
					Severity:    "major",
				})
				continue
			}
			if st == "fail" {
				failed = true
				defects = append(defects, models.InspectionDefect{
					ItemID:      itemID,
					Description: "Failed inspection item",
					Severity:    "major",
				})
			}
		}
		for _, r := range ins.Results {
			if r.Status != "fail" {
				continue
			}
			failed = true
			found := false
			for _, d := range defects {
				if d.ItemID == r.ItemID {
					found = true
					break
				}
			}
			if !found {
				desc := r.Note
				if desc == "" {
					desc = "Failed inspection item"
				}
				defects = append(defects, models.InspectionDefect{
					ItemID: r.ItemID, Description: desc, Severity: "minor",
				})
			}
		}
		ins.Defects = defects
		if failed {
			ins.Status = "failed"
		} else {
			ins.Status = "passed"
		}
		ins.SubmittedAt = nowISO()
		ins.SubmittedBy = currentUser(c, h.Repo)
		if body.Signature != "" {
			ins.Signature = body.Signature
		}
	})
	if err != nil {
		respondError(c, err)
		return
	}
	h.Repo.LogBest(ctx, "submit", "vehicle_inspection", id, updated.Status, currentUser(c, h.Repo))
	c.JSON(http.StatusOK, updated)
}

func (h *Inspections) createDefectWO(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	ins, err := h.Repo.Inspections.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if ins.Status != "failed" && len(ins.Defects) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "inspection has no defects"})
		return
	}
	if ins.MaintenanceID != "" {
		mx, err := h.Repo.Maintenance.Get(ctx, ins.MaintenanceID)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"maintenance": mx, "alreadyLinked": true})
			return
		}
	}
	notes := "DVIR defects from inspection " + ins.ID
	for _, d := range ins.Defects {
		if d.Description != "" {
			notes += "; " + d.ItemID + ": " + d.Description
		}
	}
	mx := models.MaintenanceItem{
		VehicleID: ins.VehicleID,
		Date:      todayDate(),
		Type:      "Inspection",
		Service:   "DVIR defect follow-up",
		Status:    "scheduled",
		Priority:  "high",
		Workshop:  "TBD",
		Odo:       ins.Odo,
		Notes:     notes,
	}
	created, err := h.Repo.Maintenance.Add(ctx, mx)
	if err != nil {
		respondError(c, err)
		return
	}
	_, err = h.Repo.Inspections.Update(ctx, id, func(v *models.VehicleInspection) {
		v.MaintenanceID = created.ID
	})
	if err != nil {
		respondError(c, err)
		return
	}
	h.Repo.LogBest(ctx, "defect-wo", "vehicle_inspection", id, created.ID, currentUser(c, h.Repo))
	c.JSON(http.StatusCreated, gin.H{"maintenance": created})
}

func todayDate() string {
	return nowISO()[:10]
}
