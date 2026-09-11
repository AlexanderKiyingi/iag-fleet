package handlers

import (
	"errors"
	"strings"
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

// A tyre create missing a NOT NULL field used to reach Postgres and come back
// as a 500/502 carrying the raw driver text — `null value in column
// "mounted_date" ... violates not-null constraint (SQLSTATE 23502)` — which the
// web app then showed to whoever was filling the form. These pin the field
// checks that answer first, and the sentinel that maps them to 400.
func TestValidateTyre_requiredFields(t *testing.T) {
	ok := models.Tyre{
		VehicleID: "8f14e45f-ceea-367a-9a36-dedd4bea2543", Position: "FL",
		Brand: "Michelin", Model: "XZY", Serial: "SN-1",
		MountedDate: "2026-01-02", Status: "good",
	}
	if err := validateTyre(&ok); err != nil {
		t.Fatalf("complete tyre rejected: %v", err)
	}

	for _, tc := range []struct {
		field string
		mut   func(*models.Tyre)
	}{
		{"vehicleId", func(x *models.Tyre) { x.VehicleID = "" }},
		{"position", func(x *models.Tyre) { x.Position = "" }},
		{"brand", func(x *models.Tyre) { x.Brand = "" }},
		{"model", func(x *models.Tyre) { x.Model = "" }},
		{"serial", func(x *models.Tyre) { x.Serial = "" }},
		{"mountedDate", func(x *models.Tyre) { x.MountedDate = "" }},
		{"status", func(x *models.Tyre) { x.Status = "" }},
	} {
		bad := ok
		tc.mut(&bad)
		err := validateTyre(&bad)
		if err == nil {
			t.Fatalf("missing %s accepted", tc.field)
		}
		if !errors.Is(err, errInvalidTyre) {
			t.Fatalf("missing %s: want errInvalidTyre (maps to 400), got %v", tc.field, err)
		}
		// The message has to name the field — the whole point is that the
		// operator learns which box to fill, not which column is NOT NULL.
		if !strings.Contains(err.Error(), tc.field) {
			t.Fatalf("missing %s: message does not name the field: %v", tc.field, err)
		}
	}

	// Whitespace is not a value: " " would pass a bare != "" check and then hit
	// the constraint anyway.
	blank := ok
	blank.Position = "   "
	if err := validateTyre(&blank); err == nil {
		t.Fatal("whitespace-only position accepted")
	}

	badDate := ok
	badDate.MountedDate = "02/01/2026"
	if err := validateTyre(&badDate); err == nil {
		t.Fatal("non-ISO mountedDate accepted")
	}

	// Tread depth 0 is a real reading (worn flat), not a missing value.
	worn := ok
	worn.TreadDepthMm = 0
	if err := validateTyre(&worn); err != nil {
		t.Fatalf("zero tread depth should be allowed: %v", err)
	}
	negative := ok
	negative.TreadDepthMm = -1
	if err := validateTyre(&negative); err == nil {
		t.Fatal("negative tread depth accepted")
	}
}

// kind carries a CHECK constraint (migration 0009). Without this check a blank
// kind was rejected by Postgres and surfaced as a 500 quoting the constraint
// name — which names a database object rather than the field left blank.
func TestValidateInspectionTemplate_kind(t *testing.T) {
	for _, kind := range inspectionTemplateKinds {
		if err := validateInspectionTemplate(&models.InspectionTemplate{Name: "Daily", Kind: kind}); err != nil {
			t.Fatalf("kind %q rejected: %v", kind, err)
		}
	}
	for _, bad := range []string{"", "   ", "pretrip", "PRE-TRIP", "weekly"} {
		err := validateInspectionTemplate(&models.InspectionTemplate{Name: "Daily", Kind: bad})
		if err == nil {
			t.Fatalf("kind %q accepted", bad)
		}
		if !errors.Is(err, errInvalidInspectionTemplate) {
			t.Fatalf("kind %q: want errInvalidInspectionTemplate (maps to 400), got %v", bad, err)
		}
		// The message lists the allowed values; "invalid kind" alone leaves the
		// operator guessing at a three-item set they cannot see.
		if !strings.Contains(err.Error(), "pre-trip") {
			t.Fatalf("kind %q: message does not list the allowed values: %v", bad, err)
		}
	}
	if err := validateInspectionTemplate(&models.InspectionTemplate{Kind: "pre-trip"}); err == nil {
		t.Fatal("missing name accepted")
	}
}
