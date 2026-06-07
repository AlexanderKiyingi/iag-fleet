package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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

func TestRespondVehicleOr500_notFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondVehicleOr500(c, errUnknownVehicle)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
}
