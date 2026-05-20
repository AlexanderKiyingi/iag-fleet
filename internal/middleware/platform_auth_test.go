package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAttachPrincipal_skipsGatewaySecretOnReady(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mw := NewPlatformAuth(PlatformAuthOptions{
		Mode:          "gateway",
		GatewaySecret: "test-secret-min-16-chars",
	})

	r := gin.New()
	r.Use(mw.AttachPrincipal())
	r.GET("/ready", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /ready = %d, want 200 (no gateway headers)", w.Code)
	}
}

func TestAttachPrincipal_requiresGatewaySecretOnAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mw := NewPlatformAuth(PlatformAuthOptions{
		Mode:          "gateway",
		GatewaySecret: "test-secret-min-16-chars",
	})

	r := gin.New()
	r.Use(mw.AttachPrincipal())
	r.GET("/api/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/ping without secret = %d, want 401", w.Code)
	}
}
