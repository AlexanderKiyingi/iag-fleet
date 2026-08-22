package auth

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/alvor-technologies/iag-platform-go/apierr"
	"github.com/iag/fleet-tool/backend/internal/ctxkeys"
)

// HasPerm checks platform JWT permissions (fleet.* and legacy unprefixed aliases).
func HasPerm(c *gin.Context, codename string) bool {
	claims, ok := platformClaims(c)
	if !ok {
		return false
	}
	if claims.IsSuperuser {
		return true
	}
	if platformAdminMayView(claims, codename) {
		return true
	}
	// Staff reads, staff does not write.
	//
	// This used to grant every fleet permission — including delete_* — to any
	// token with is_staff, before the permission list was consulted at all. No
	// other service does that: the gateway authorizer, finance, crm and
	// warehouse all short-circuit on is_superuser only. Fleet was alone in
	// treating staff as a master key.
	//
	// Removing the write half is safe rather than a lockout risk. Every
	// /api/v1/fleet/api route already requires the fleet platform-access
	// permission plus a fleet view/mutate codename at the gateway, so a staff
	// caller arriving through it necessarily holds real permissions that the
	// checks below honour. What this closes is direct service access — the
	// railway.internal path, a future public route, a gateway rule that slips —
	// where a staff token previously got full control of the fleet.
	//
	// Read stays, scoped exactly like the platform-admin path above it, because
	// that is the case staff plausibly relies on and it cannot mutate anything.
	if claims.IsStaff && isFleetViewCodename(codename) {
		return true
	}
	perms := platformPerms(c)
	if len(perms) == 0 {
		return !isStrictRBAC(c)
	}
	for _, want := range []string{codename, fleetAlias(codename), legacyAlias(codename)} {
		if claims.HasPermission(want) {
			return true
		}
	}
	for _, p := range perms {
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
		apierr.Forbidden(c, "staff access required")
	}
}
