package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alvor-technologies/iag-platform-go/apierr"
)

// RequireUser blocks anonymous access (platform principal required).
func RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsAuthenticated(c) {
			apierr.Unauthorized(c, "authentication required")
			return
		}
		c.Next()
	}
}

// RequirePerm gates a route on a fleet permission codename.
func RequirePerm(codename string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsAuthenticated(c) {
			apierr.Unauthorized(c, "authentication required")
			return
		}
		if !HasPerm(c, codename) {
			apierr.WriteWith(c, http.StatusForbidden, apierr.CodeForbidden,
				"permission denied: "+codename, gin.H{"required_permission": codename})
			return
		}
		c.Next()
	}
}

// RequireAnyPerm passes if the user holds at least one codename.
func RequireAnyPerm(codenames ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsAuthenticated(c) {
			apierr.Unauthorized(c, "authentication required")
			return
		}
		for _, cn := range codenames {
			if HasPerm(c, cn) {
				c.Next()
				return
			}
		}
		apierr.WriteWith(c, http.StatusForbidden, apierr.CodeForbidden,
			"permission denied", gin.H{"required_permission": codenames})
	}
}

// fleetViewPerms enumerates every fleet read codename. It mirrors the seeded
// view_* catalogue (iag-authentication fleetCatalogue) plus view_telemetry.
var fleetViewPerms = []string{
	"view_vehicle", "view_driver", "view_jmp", "view_cargo", "view_cargo_doc",
	"view_maintenance_item", "view_part", "view_tyre", "view_trip",
	"view_safety_event", "view_compliance_item", "view_service_request",
	"view_task_item", "view_deployment_day", "view_fuel_record", "view_fuel_request",
	"view_vehicle_inspection", "view_pm_schedule", "view_telemetry",
}

// RequireAnyFleetView gates aggregate/summary endpoints (dashboard, analytics,
// reports, calendar) that re-expose entity data the per-entity list endpoints
// already gate on view_*. It passes if the principal holds at least one fleet
// read permission, so an authenticated principal with no fleet permissions
// (e.g. a user scoped only to another domain) can't read fleet data in
// summarized form.
func RequireAnyFleetView() gin.HandlerFunc {
	return RequireAnyPerm(fleetViewPerms...)
}

// RequireSuperuser blocks any non-superuser.
func RequireSuperuser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if claims, ok := platformClaims(c); ok && claims.IsSuperuser {
			c.Next()
			return
		}
		if !IsAuthenticated(c) {
			apierr.Unauthorized(c, "authentication required")
			return
		}
		apierr.Forbidden(c, "superuser access required")
	}
}
