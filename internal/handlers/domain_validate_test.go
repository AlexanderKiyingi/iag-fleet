package handlers

import (
	"testing"
	"time"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

func TestDriverPermitOK(t *testing.T) {
	today := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	if store.DriverPermitOK(models.Driver{PermitExpiry: "2026-06-06"}, today) {
		t.Fatal("expired permit should fail")
	}
	if !store.DriverPermitOK(models.Driver{PermitExpiry: "2026-12-01"}, today) {
		t.Fatal("future permit should pass")
	}
}

func TestValidateDriver_certRequirements(t *testing.T) {
	if err := validateDriver(&models.Driver{FirstAid: true}); err == nil {
		t.Fatal("expected firstAidExpiry requirement")
	}
	if err := validateDriver(&models.Driver{Defensive: true}); err == nil {
		t.Fatal("expected defensiveExpiry requirement")
	}
}

func TestValidateMaintenanceStatus(t *testing.T) {
	if err := validateMaintenanceStatus("bogus"); err == nil {
		t.Fatal("expected invalid status error")
	}
	if err := validateMaintenanceStatus("completed"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFutureExpiry(t *testing.T) {
	if err := validateFutureExpiry("2020-01-01"); err == nil {
		t.Fatal("past expiry should fail renew")
	}
}
