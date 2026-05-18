package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alvor-technologies/iag-authclient"
	"github.com/iag/fleet-tool/backend/internal/ctxkeys"
)

const ctxAuthModeKey = "auth.mode"

func SetMode(c *gin.Context, mode string) {
	c.Set(ctxAuthModeKey, mode)
}

// HasPerm checks platform JWT permissions (fleet.* and legacy unprefixed aliases).
func HasPerm(c *gin.Context, codename string) bool {
	claims, ok := platformClaims(c)
	if !ok {
		return false
	}
	if claims.IsSuperuser {
		return true
	}
	for _, want := range []string{codename, fleetAlias(codename), legacyAlias(codename)} {
		if authclient.HasPermission(claims, want) {
			return true
		}
	}
	for _, p := range platformPerms(c) {
		for _, want := range []string{codename, fleetAlias(codename), legacyAlias(codename)} {
			if p == want {
				return true
			}
		}
	}
	return false
}

func IsAuthenticated(c *gin.Context) bool {
	_, ok := platformUserID(c)
	return ok
}

func ActorUserKey(c *gin.Context) (string, bool) {
	if id, ok := platformUserID(c); ok {
		return id.String(), true
	}
	return "", false
}

func OperatorName(c *gin.Context) string {
	if claims, ok := platformClaims(c); ok {
		if claims.Email != "" {
			return claims.Email
		}
		return claims.Subject
	}
	return ""
}

func PlatformUserID(c *gin.Context) (uuid.UUID, bool) {
	return platformUserID(c)
}

func PlatformClaimsFromContext(c *gin.Context) (*authclient.Claims, bool) {
	return platformClaims(c)
}

func platformUserID(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxkeys.UserID)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}

func platformClaims(c *gin.Context) (*authclient.Claims, bool) {
	v, ok := c.Get(ctxkeys.Claims)
	if !ok {
		return nil, false
	}
	cl, ok := v.(*authclient.Claims)
	return cl, ok
}

func platformPerms(c *gin.Context) []string {
	v, ok := c.Get(ctxkeys.Permissions)
	if !ok {
		return nil
	}
	list, _ := v.([]string)
	return list
}

func fleetAlias(codename string) string {
	if strings.HasPrefix(codename, "fleet.") {
		return codename
	}
	return "fleet." + codename
}

func legacyAlias(codename string) string {
	return strings.TrimPrefix(codename, "fleet.")
}

func RequireStaff() gin.HandlerFunc {
	return func(c *gin.Context) {
		if claims, ok := platformClaims(c); ok && (claims.IsStaff || claims.IsSuperuser) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "staff access required"})
	}
}
