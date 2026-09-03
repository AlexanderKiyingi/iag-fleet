package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// The route table must match what the frontend calls even on a deployment with
// no telemetry store, so the map gets a 503 it can explain rather than a 404
// that looks like the endpoint was never built. Every other IoT route is
// registered this way for the same reason.
func TestGeofencePOIs_noStoreIs503NotMissingRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	// Register directly rather than through Register(): the real registration
	// wraps each route in a permission check, and this case is about the store
	// guard, not authorisation.
	h := &IoT{Store: nil}
	api.GET("/geofence-pois", h.requireStore, h.geofencePOIs)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/geofence-pois", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (telemetry store not configured)", w.Code)
	}
	if w.Body.String() == "" {
		t.Error("503 carried no explanation")
	}
}

// The wire shape is what the map draws. `radiusKm` in particular has to keep
// its name and unit: the client multiplies by 1000 for Leaflet, so renaming or
// silently switching to metres would draw every fence a thousand times too big.
func TestGeofencePOIWireShape(t *testing.T) {
	body, err := json.Marshal(geofencePOI{
		Name: "Mombasa Port", Lat: -4.05, Lng: 39.667, Type: "port", RadiusKm: 1.5,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"name", "lat", "lng", "type", "radiusKm"} {
		if _, ok := got[key]; !ok {
			t.Errorf("wire shape is missing %q; the map reads this key", key)
		}
	}
	if got["radiusKm"] != 1.5 {
		t.Errorf("radiusKm = %v, want 1.5 (kilometres, not metres)", got["radiusKm"])
	}
}
