package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Platform exposes profile + notification registration for platform auth.
type Platform struct {
	Repo *store.Repository
}

func (p *Platform) Register(rg *gin.RouterGroup) {
	rg.GET("/users/me", auth.RequireUser(), p.me)
}

func (p *Platform) me(c *gin.Context) {
	claims, ok := auth.PlatformClaimsFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	userID, _ := auth.PlatformUserID(c)
	if p.Repo != nil {
		_ = p.Repo.Notifications.RegisterRecipient(c.Request.Context(), userID.String(), claims.Email)
	}
	c.JSON(http.StatusOK, gin.H{
		"mode": "platform",
		"user": gin.H{
			"id":          userID.String(),
			"email":       claims.Email,
			"isStaff":     claims.IsStaff,
			"isSuperuser": claims.IsSuperuser,
			"groups":      claims.Groups,
		},
		"permissions": claims.Permissions,
	})
}
