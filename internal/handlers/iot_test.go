package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRequireVehicleForTrack_nilRepoSkipsValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &IoT{Repo: nil}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/vehicles/UNKNOWN/track", nil)

	if !h.requireVehicleForTrack(c, "UNKNOWN") {
		t.Fatal("nil repo should skip vehicle validation")
	}
}

func TestRespondIotError_duplicateSerial409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondIotError(c, &pgconn.PgError{Code: "23505", ConstraintName: "iot_devices_serial_key"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", w.Code)
	}
}

func TestRespondIotError_duplicateActiveDevice409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondIotError(c, &pgconn.PgError{Code: "23505", ConstraintName: "iot_devices_one_active_per_vehicle"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "active device") {
		t.Fatalf("body %q, want vehicle-already-bound message", w.Body.String())
	}
}

func TestRespondVehicleOr500_notFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondVehicleOr500(c, errUnknownVehicle)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
}

// The guide is what an operator follows to point a tracker at the platform. It
// used to fall back to the compose-internal name when TELEMETRY_INGEST_URL was
// unset, so an unconfigured deployment handed out http://fleet-iot-ingest:4080
// — unreachable from a GPRS SIM, and failing in a way nobody sees: the device
// just never connects. Reporting no address is the honest answer.
func TestIngestionGuide_unsetIngestURLReportsNoAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TELEMETRY_INGEST_URL", "")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/iot/ingestion", nil)
	(&IoT{}).ingestionGuide(c)

	body := w.Body.String()
	if strings.Contains(body, "fleet-iot-ingest") {
		t.Fatalf("guide invented an unroutable address: %s", body)
	}
	if !strings.Contains(body, `"configured":false`) {
		t.Fatalf("guide did not report the address as unconfigured: %s", body)
	}
	if strings.Contains(body, `"url"`) {
		t.Fatalf("guide gave a url with nothing configured: %s", body)
	}
}

func TestIngestionGuide_setIngestURLIsReported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TELEMETRY_INGEST_URL", "https://ingest.example.test/")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/iot/ingestion", nil)
	(&IoT{}).ingestionGuide(c)

	body := w.Body.String()
	// Trailing slash trimmed, and both the current and legacy paths given.
	if !strings.Contains(body, "https://ingest.example.test/v1/pings") {
		t.Fatalf("ingest url missing or malformed: %s", body)
	}
	if !strings.Contains(body, `"configured":true`) {
		t.Fatalf("guide did not report the address as configured: %s", body)
	}
}
