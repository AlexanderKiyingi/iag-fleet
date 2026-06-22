package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/models"
)

func bodyContext(body string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	return c
}

// create (POST) and replace (PUT) bind whole models directly; they must apply
// the same string→scalar coercion as PATCH so a form posting quoted numbers
// doesn't 400 on the bind.
func TestBindJSONCoercedScalars(t *testing.T) {
	var v models.Vehicle
	c := bodyContext(`{"id":"VEH-1","lat":"0.347","year":"2021","fuelTracker":"true"}`)
	if err := bindJSONCoerced(c, &v); err != nil {
		t.Fatalf("bindJSONCoerced error: %v", err)
	}
	if v.Lat != 0.347 || v.Year != 2021 || !v.FuelTracker {
		t.Errorf("not coerced: lat=%v year=%d fuelTracker=%v", v.Lat, v.Year, v.FuelTracker)
	}
}

func TestBindJSONCoercedSliceScalars(t *testing.T) {
	var vs []models.Vehicle
	c := bodyContext(`[{"id":"A","lat":"0.1"},{"id":"B","lat":"0.2"}]`)
	if err := bindJSONCoercedSlice(c, &vs); err != nil {
		t.Fatalf("bindJSONCoercedSlice error: %v", err)
	}
	if len(vs) != 2 || vs[0].Lat != 0.1 || vs[1].Lat != 0.2 {
		t.Errorf("slice not coerced: %+v", vs)
	}
}

// The Next.js store submits form values as strings, so a PATCH can carry
// quoted numbers/bools for plain numeric columns. mergeJSON must coerce them
// to the struct field's scalar type rather than 400 on the map→struct
// unmarshal (the "cannot unmarshal string into Go struct field" bug).
func TestMergeJSONCoercesStringScalars(t *testing.T) {
	existing := models.Vehicle{ID: "VEH-1", Plate: "OLD", Lat: 1, Lng: 2}

	patch := []byte(`{
		"lat": "0.347",
		"lng": "32.581",
		"year": "2021",
		"fuelTracker": "true",
		"seatCapacity": "14",
		"plate": "NEW"
	}`)

	merged, err := mergeJSON(existing, patch)
	if err != nil {
		t.Fatalf("mergeJSON returned error: %v", err)
	}

	if merged.Lat != 0.347 || merged.Lng != 32.581 {
		t.Errorf("coords not coerced: lat=%v lng=%v", merged.Lat, merged.Lng)
	}
	if merged.Year != 2021 {
		t.Errorf("int field not coerced: year=%d", merged.Year)
	}
	if !merged.FuelTracker {
		t.Errorf("bool field not coerced: fuelTracker=%v", merged.FuelTracker)
	}
	if merged.SeatCapacity == nil || *merged.SeatCapacity != 14 {
		t.Errorf("pointer int field not coerced: seatCapacity=%v", merged.SeatCapacity)
	}
	// Genuine string field still merges verbatim.
	if merged.Plate != "NEW" {
		t.Errorf("string field clobbered: plate=%q", merged.Plate)
	}
	// Untouched field retained from existing.
	if merged.ID != "VEH-1" {
		t.Errorf("unrelated field lost: id=%q", merged.ID)
	}
}

// Editing a record whose numeric columns are unset posts them as blank
// strings ({"lat":""}). Those must null out (value field → zero, pointer
// field → nil) instead of failing the unmarshal with the "cannot unmarshal
// string into Go struct field Vehicle.lat of type float64" error.
func TestMergeJSONCoercesEmptyStringScalars(t *testing.T) {
	existing := models.Vehicle{ID: "VEH-1", Lat: 1, Lng: 2, Year: 2019}
	seat := 14
	existing.SeatCapacity = &seat

	patch := []byte(`{"lat":"","lng":"","year":"","fuelTracker":"","seatCapacity":""}`)

	merged, err := mergeJSON(existing, patch)
	if err != nil {
		t.Fatalf("mergeJSON returned error: %v", err)
	}
	if merged.Lat != 0 || merged.Lng != 0 || merged.Year != 0 {
		t.Errorf("blank value fields not zeroed: lat=%v lng=%v year=%d", merged.Lat, merged.Lng, merged.Year)
	}
	if merged.FuelTracker {
		t.Errorf("blank bool not zeroed: fuelTracker=%v", merged.FuelTracker)
	}
	if merged.SeatCapacity != nil {
		t.Errorf("blank pointer field not nilled: seatCapacity=%v", merged.SeatCapacity)
	}
}

// The bind path (POST/PUT) must tolerate blank numeric strings the same way.
func TestBindJSONCoercedEmptyStringScalars(t *testing.T) {
	var v models.Vehicle
	c := bodyContext(`{"id":"VEH-1","lat":"","year":"","seatCapacity":""}`)
	if err := bindJSONCoerced(c, &v); err != nil {
		t.Fatalf("bindJSONCoerced error on blank scalars: %v", err)
	}
	if v.Lat != 0 || v.Year != 0 || v.SeatCapacity != nil {
		t.Errorf("blanks not nulled: lat=%v year=%d seat=%v", v.Lat, v.Year, v.SeatCapacity)
	}
}

// A real number in the patch must keep working unchanged.
func TestMergeJSONKeepsNumericValues(t *testing.T) {
	existing := models.Vehicle{ID: "VEH-2"}
	merged, err := mergeJSON(existing, []byte(`{"lat": 0.5, "year": 2020}`))
	if err != nil {
		t.Fatalf("mergeJSON returned error: %v", err)
	}
	if merged.Lat != 0.5 || merged.Year != 2020 {
		t.Errorf("numeric passthrough failed: lat=%v year=%d", merged.Lat, merged.Year)
	}
}
