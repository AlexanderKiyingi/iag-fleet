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
