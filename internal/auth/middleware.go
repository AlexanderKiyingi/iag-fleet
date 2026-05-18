package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireUser blocks anonymous access (platform principal required).
func RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsAuthenticated(c) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Next()
	}
}

// RequirePerm gates a route on a fleet permission codename.
func RequirePerm(codename string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsAuthenticated(c) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if !HasPerm(c, codename) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "permission denied",
				"need":  codename,
			})
			return
		}
		c.Next()
	}
}

// RequireAnyPerm passes if the user holds at least one codename.
func RequireAnyPerm(codenames ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsAuthenticated(c) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		for _, cn := range codenames {
			if HasPerm(c, cn) {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":   "permission denied",
			"needAny": codenames,
		})
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
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "superuser only"})
	}
}
