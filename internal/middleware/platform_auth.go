// Package middleware implements Bearer+aud authentication for inbound fleet
// requests. The gateway-header trust path (X-IAG-* + GATEWAY_INTERNAL_SECRET)
// has been removed — every request must carry a verifiable JWT with
// aud=iag.fleet.
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alvor-technologies/iag-platform-go/apierr"
	"github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/iag/fleet-tool/backend/internal/ctxkeys"
)

type PlatformAuth struct {
	verifier *authclient.Verifier
}

type PlatformAuthOptions struct {
	Verifier *authclient.Verifier
}

func NewPlatformAuth(opts PlatformAuthOptions) *PlatformAuth {
	return &PlatformAuth{verifier: opts.Verifier}
}

func isPublicProbePath(path string) bool {
	switch path {
	case "/health", "/healthz", "/ready":
		return true
	default:
		return false
	}
}

// AttachPrincipal validates a Bearer token (if present) and pins the claims +
// user UUID onto the Gin context. Anonymous requests pass through; handlers
// use auth.RequireUser / RequirePerm to reject them.
func (m *PlatformAuth) AttachPrincipal() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isPublicProbePath(c.Request.URL.Path) {
			c.Next()
			return
		}
		if m.verifier == nil {
			apierr.Write(c, http.StatusServiceUnavailable, apierr.CodeServiceUnavailable, "JWT verifier not configured")
			return
		}
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.Next()
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")
		claims, err := m.verifier.Verify(tokenStr)
		if err != nil {
			apierr.Unauthorized(c, "invalid or expired token")
			return
		}
		// User principals must carry a UUID subject — reject malformed ones
		// (mirrors pre-migration behavior). Service principals (client_id
		// subject) are accepted but get no UserID set, so auth.IsAuthenticated
		// and the RequireUser/RequirePerm gates correctly treat them as
		// non-user callers and 401.
		var userID uuid.UUID
		if claims.IsUser() {
			id, err := uuid.Parse(claims.Subject)
			if err != nil {
				apierr.Unauthorized(c, "invalid token subject")
				return
			}
			userID = id
		}
		setPrincipal(c, userID, claims, claims.Permissions)
		c.Next()
	}
}

func (m *PlatformAuth) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := UserID(c); !ok {
			apierr.Unauthorized(c, "authentication required")
			return
		}
		c.Next()
	}
}

func setPrincipal(c *gin.Context, userID uuid.UUID, claims *authclient.Claims, perms []string) {
	// Only set UserID for user principals — auth.IsAuthenticated and the
	// RequireUser gates key off the presence of this entry to distinguish
	// human users from service-principal callers.
	if userID != uuid.Nil {
		c.Set(ctxkeys.UserID, userID)
	}
	c.Set(ctxkeys.Claims, claims)
	c.Set(ctxkeys.Permissions, perms)
}

func UserID(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxkeys.UserID)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}

func PlatformClaims(c *gin.Context) (*authclient.Claims, bool) {
	v, ok := c.Get(ctxkeys.Claims)
	if !ok {
		return nil, false
	}
	cl, ok := v.(*authclient.Claims)
	return cl, ok
}

func Permissions(c *gin.Context) []string {
	v, ok := c.Get(ctxkeys.Permissions)
	if !ok {
		return nil
	}
	list, _ := v.([]string)
	return list
}
