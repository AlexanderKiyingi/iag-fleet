package handlers

import (
	"testing"

	"github.com/iag/fleet-iot/iot"
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
