package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/iag/fleet-tool/backend/internal/ctxkeys"
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

// setPrincipal must omit ctxkeys.UserID for service-principal callers so
// auth.IsAuthenticated (and the RequireUser/RequirePerm gates that depend on
// it) correctly treat service tokens as non-user.
func TestSetPrincipal_skipsUserIDForServicePrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	claims := &authclient.Claims{PrincipalType: authclient.PrincipalService}

	setPrincipal(c, uuid.Nil, claims, []string{"fleet.view_vehicle"})

	if _, ok := c.Get(ctxkeys.UserID); ok {
		t.Fatalf("UserID key set for zero UUID; want absent (would make IsAuthenticated true for service tokens)")
	}
	if _, ok := c.Get(ctxkeys.Claims); !ok {
		t.Fatal("Claims key missing; permissions/HasPerm would be empty on service tokens")
	}
	if _, ok := c.Get(ctxkeys.Permissions); !ok {
		t.Fatal("Permissions key missing")
	}
}

// setPrincipal must set UserID for user principals so RequireUser passes.
func TestSetPrincipal_setsUserIDForUserPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	uid := uuid.New()

	setPrincipal(c, uid, &authclient.Claims{PrincipalType: authclient.PrincipalUser}, nil)

	got, ok := UserID(c)
	if !ok || got != uid {
		t.Fatalf("UserID(c) = %v, %v; want %v, true", got, ok, uid)
	}
}
