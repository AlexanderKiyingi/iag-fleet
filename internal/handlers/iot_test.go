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
	// Trailing slash trimmed. `url` is the one an operator programs, and it is
	// /api/iot/pings because that path works both directly and through the
	// gateway; /v1/pings is offered separately as direct-only.
	if !strings.Contains(body, `"url":"https://ingest.example.test/api/iot/pings"`) {
		t.Fatalf("ingest url missing or malformed: %s", body)
	}
	if !strings.Contains(body, `"directPath":"https://ingest.example.test/v1/pings"`) {
		t.Fatalf("direct path missing: %s", body)
	}
	if !strings.Contains(body, `"configured":true`) {
		t.Fatalf("guide did not report the address as configured: %s", body)
	}
}

// The deployed topology fronts ingest with the API gateway, which routes only
// /api/v1/fleet/api/iot/pings (rewritten to /api/iot/pings) and has no route for
// /v1/pings. The guide used to advertise /v1/pings as the primary URL, so an
// operator following it against the gateway base would program a device against
// a 404 — the same class of failure as the unroutable compose hostname above:
// silent, and indistinguishable from a fleet that simply never reports.
func TestIngestionGuide_gatewayBaseYieldsRoutableURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TELEMETRY_INGEST_URL", "https://gw.example.test/api/v1/fleet")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/iot/ingestion", nil)
	(&IoT{}).ingestionGuide(c)

	body := w.Body.String()
	const want = `"url":"https://gw.example.test/api/v1/fleet/api/iot/pings"`
	if !strings.Contains(body, want) {
		t.Fatalf("guide did not hand out the gateway-routable endpoint: %s", body)
	}
}
