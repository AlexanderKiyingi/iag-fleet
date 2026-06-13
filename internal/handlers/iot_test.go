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
