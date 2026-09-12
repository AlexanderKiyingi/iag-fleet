package handlers

import (
	"context"
	"testing"

	"github.com/iag/fleet-iot/iot"
	fleetstore "github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// The rules a fence has to satisfy before it is worth storing.
//
// These exist in the table as CHECK constraints too, but a constraint violation
// arrives as a 500 naming geofence_pois_radius_positive, which tells an operator
// nothing about what to type instead.
func TestValidateGeofencePOI(t *testing.T) {
	ok := iot.GeofencePOIRecord{
		GeofencePOI: iot.GeofencePOI{Name: "New Depot", Lat: 0.32, Lng: 32.58, Type: "site", RadiusKm: 0.4},
		IsActive:    true,
	}
	if msg := validateGeofencePOI(ok); msg != "" {
		t.Fatalf("a valid fence was rejected: %s", msg)
	}

	cases := []struct {
		name  string
		mutit func(*iot.GeofencePOIRecord)
		want  string
	}{
		{"no name", func(p *iot.GeofencePOIRecord) { p.Name = "" }, "name is required"},
		{"lat out of range", func(p *iot.GeofencePOIRecord) { p.Lat = 91 }, "lat must be between -90 and 90"},
		{"lng out of range", func(p *iot.GeofencePOIRecord) { p.Lng = 181 }, "lng must be between -180 and 180"},
		{
			// The dangerous one: zero stores cleanly and the fence then never
			// triggers, so the site looks monitored and silently is not.
			"zero radius",
			func(p *iot.GeofencePOIRecord) { p.RadiusKm = 0 },
			"radiusKm must be greater than 0",
		},
		{"negative radius", func(p *iot.GeofencePOIRecord) { p.RadiusKm = -1 }, "radiusKm must be greater than 0"},
		{
			// Same sentinel the ingest path rejects: 0,0 is "no fix", and a
			// fence there would sit in the Gulf of Guinea catching nothing.
			"null island",
			func(p *iot.GeofencePOIRecord) { p.Lat, p.Lng = 0, 0 },
			"lat/lng 0,0 is the no-fix sentinel, not a location",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := ok
			tc.mutit(&p)
			if got := validateGeofencePOI(p); got != tc.want {
				t.Fatalf("validate = %q, want %q", got, tc.want)
			}
		})
	}
}

// A fence exactly on a boundary is legal — the checks are range checks, not
// exclusive ones, and rejecting the poles or the date line would be arbitrary.
func TestValidateGeofencePOIBoundaries(t *testing.T) {
	for _, p := range []iot.GeofencePOIRecord{
		{GeofencePOI: iot.GeofencePOI{Name: "north", Lat: 90, Lng: 0, RadiusKm: 1}},
		{GeofencePOI: iot.GeofencePOI{Name: "dateline", Lat: 10, Lng: 180, RadiusKm: 1}},
		{GeofencePOI: iot.GeofencePOI{Name: "tiny", Lat: 1, Lng: 1, RadiusKm: 0.001}},
	} {
		if msg := validateGeofencePOI(p); msg != "" {
			t.Fatalf("%s rejected: %s", p.Name, msg)
		}
	}
}

// A rename must not quietly un-scope the fence.
//
// The handler used to rename by creating the new row and deleting the old one.
// geofence_vehicles references the name ON DELETE CASCADE, so that sequence
// dropped every vehicle assigned to the fence — and since no rows means EVERY
// vehicle, the fence came back fleet-wide. Renaming "Client A Depot" would have
// silently started raising arrivals for all 37 trucks.
func TestIntegration_GeofenceRenameKeepsAssignments(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	ctx := context.Background()
	store := iot.NewStore(pool)

	v := integrationVehicle(testID("VEH-GEO"), "GEO-01")
	repo := fleetstore.NewRepository(pool)
	if _, err := repo.Vehicles.Add(ctx, v); err != nil {
		t.Fatalf("seed vehicle: %v", err)
	}

	fence := iot.GeofencePOIRecord{
		GeofencePOI: iot.GeofencePOI{
			Name: testID("FENCE-A"), Lat: 0.32, Lng: 32.58, Type: "site", RadiusKm: 0.5,
			Rule: iot.RuleStayInside,
		},
		IsActive: true,
	}
	if err := store.UpsertGeofencePOI(ctx, fence); err != nil {
		t.Fatalf("create fence: %v", err)
	}
	if err := store.SetGeofenceVehicles(ctx, fence.Name, []string{v.ID}); err != nil {
		t.Fatalf("assign vehicle: %v", err)
	}

	renamedTo := testID("FENCE-B")
	ok, err := store.RenameGeofencePOI(ctx, fence.Name, renamedTo)
	if err != nil || !ok {
		t.Fatalf("rename: ok=%v err=%v", ok, err)
	}

	all, err := store.ListGeofencePOIs(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got *iot.GeofencePOIRecord
	for i := range all {
		if all[i].Name == renamedTo {
			got = &all[i]
		}
		if all[i].Name == fence.Name {
			t.Fatal("the old name still exists — rename left a duplicate fence")
		}
	}
	if got == nil {
		t.Fatalf("renamed fence %q not found", renamedTo)
	}
	if len(got.VehicleIDs) != 1 || got.VehicleIDs[0] != v.ID {
		t.Fatalf("assignments after rename = %v, want [%s] — the fence silently went fleet-wide",
			got.VehicleIDs, v.ID)
	}
	// The rule has to survive too, or a restricted zone quietly becomes a
	// fence that merely logs.
	if got.Rule != iot.RuleStayInside {
		t.Fatalf("rule after rename = %q, want %q", got.Rule, iot.RuleStayInside)
	}
}

// Clearing the scope returns the fence to every vehicle, which is a meaningful
// state rather than an empty one.
func TestIntegration_GeofenceClearingAssignmentsGoesFleetWide(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	ctx := context.Background()
	store := iot.NewStore(pool)
	repo := fleetstore.NewRepository(pool)

	v := integrationVehicle(testID("VEH-GEO2"), "GEO-02")
	if _, err := repo.Vehicles.Add(ctx, v); err != nil {
		t.Fatalf("seed vehicle: %v", err)
	}
	name := testID("FENCE-C")
	if err := store.UpsertGeofencePOI(ctx, iot.GeofencePOIRecord{
		GeofencePOI: iot.GeofencePOI{Name: name, Lat: 0.32, Lng: 32.58, Type: "site", RadiusKm: 0.5},
		IsActive:    true,
	}); err != nil {
		t.Fatalf("create fence: %v", err)
	}
	if err := store.SetGeofenceVehicles(ctx, name, []string{v.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := store.SetGeofenceVehicles(ctx, name, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}

	all, _ := store.ListGeofencePOIs(ctx)
	for _, p := range all {
		if p.Name != name {
			continue
		}
		if len(p.VehicleIDs) != 0 {
			t.Fatalf("assignments = %v, want none", p.VehicleIDs)
		}
		if !p.AppliesTo("any-vehicle-at-all") {
			t.Fatal("a fence with no assignments must apply to every vehicle")
		}
		return
	}
	t.Fatalf("fence %q not found", name)
}
