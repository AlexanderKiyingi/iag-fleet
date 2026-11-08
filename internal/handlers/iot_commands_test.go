package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-iot/iot"
)

func TestCommandModels_emptyUntilEncoderRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/iot/command-models", nil)
	(&IoT{}).commandModels(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Models  []string `json:"models"`
		Enabled bool     `json:"enabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Enabled {
		t.Fatalf("expected enabled=false with no production encoders, body=%s", w.Body.String())
	}
	if len(body.Models) != 0 {
		t.Fatalf("expected empty models, got %v", body.Models)
	}
}

func TestEnqueueForDevice_refusesWithoutEncoder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/iot/devices/1/commands",
		strings.NewReader(`{"kind":"immobilize"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h := &IoT{}
	d := &iot.Device{ID: 1, Serial: "IMEI", Model: "D11", VehicleID: "00000000-0000-0000-0000-000000000001", IsActive: true}
	h.enqueueForDevice(c, d)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no verified encoder") && !strings.Contains(w.Body.String(), "ErrNoEncoder") && !strings.Contains(w.Body.String(), "no verified") {
		// EncodeCommand wraps ErrNoEncoder with model name.
		if !strings.Contains(strings.ToLower(w.Body.String()), "encoder") {
			t.Fatalf("body should explain missing encoder: %s", w.Body.String())
		}
	}
}

func TestEnqueueForDevice_rejectsUnknownKind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/iot/devices/1/commands",
		strings.NewReader(`{"kind":"honk"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h := &IoT{}
	d := &iot.Device{ID: 1, Model: "D11", VehicleID: "v1", IsActive: true}
	h.enqueueForDevice(c, d)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", w.Code, w.Body.String())
	}
}
