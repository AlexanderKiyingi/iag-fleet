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
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "jwt verifier not configured"})
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
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		// claims.Subject is a UUID for user principals and the client_id for
		// service principals. UserID is left zero when it isn't a UUID.
		var userID uuid.UUID
		if claims.IsUser() {
			if id, err := uuid.Parse(claims.Subject); err == nil {
				userID = id
			}
		}
		setPrincipal(c, userID, claims, claims.Permissions)
		c.Next()
	}
}

func (m *PlatformAuth) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := UserID(c); !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Next()
	}
}

func setPrincipal(c *gin.Context, userID uuid.UUID, claims *authclient.Claims, perms []string) {
	c.Set(ctxkeys.UserID, userID)
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
