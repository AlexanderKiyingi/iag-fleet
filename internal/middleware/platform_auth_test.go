package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAttachPrincipal_passesProbePathsWithoutToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mw := NewPlatformAuth(PlatformAuthOptions{})

	r := gin.New()
	r.Use(mw.AttachPrincipal())
	r.GET("/ready", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /ready = %d, want 200 (no token)", w.Code)
	}
}

func TestAttachPrincipal_503WhenVerifierMissingOnAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// No verifier configured — protected routes should fail closed.
	mw := NewPlatformAuth(PlatformAuthOptions{})

	r := gin.New()
	r.Use(mw.AttachPrincipal())
	r.GET("/api/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/ping without verifier = %d, want 503", w.Code)
	}
}
