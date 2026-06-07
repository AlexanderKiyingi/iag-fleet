package handlers

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// NewComplianceResource returns compliance CRUD with validation and status derivation.
func NewComplianceResource(repo *store.Repository) *Resource[models.ComplianceItem, *models.ComplianceItem] {
	r := &Resource[models.ComplianceItem, *models.ComplianceItem]{
		Repo:       repo,
		Collection: repo.Compliance,
		Entity:     "compliance_item",
		IDPrefix:   "CMP",
	}
	applyStatus := func(ci *models.ComplianceItem) {
		if ci.Expiry != "" {
			ci.Status = store.ComplianceStatusFromExpiry(ci.Expiry, time.Now().UTC(), store.ComplianceExpiringWithinDays)
		} else if ci.Status == "" {
			ci.Status = "missing"
		}
	}
	r.BeforeCreate = func(c *gin.Context, item *models.ComplianceItem) error {
		if err := validateComplianceItem(item); err != nil {
			return err
		}
		applyStatus(item)
		return nil
	}
	r.BeforeUpdate = func(c *gin.Context, item *models.ComplianceItem) error {
		if err := validateComplianceItem(item); err != nil {
			return err
		}
		applyStatus(item)
		return nil
	}
	r.AfterCreate = func(ctx context.Context, item models.ComplianceItem) {}
	return r
}
