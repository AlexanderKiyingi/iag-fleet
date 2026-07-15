package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// maxAvatarLen caps the avatar field defensively — it may carry a base64
// data: URL, so reject anything unreasonably large before it hits the DB.
const maxAvatarLen = 1_500_000

// Platform exposes profile + notification registration for platform auth.
type Platform struct {
	Repo *store.Repository
}

func (p *Platform) Register(rg *gin.RouterGroup) {
	rg.GET("/users/me", auth.RequireUser(), p.me)
	rg.PUT("/users/me/profile", auth.RequireUser(), p.updateProfile)
}

func (p *Platform) me(c *gin.Context) {
	claims, ok := auth.PlatformClaimsFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	userID, _ := auth.PlatformUserID(c)
	perms := auth.EffectivePermissions(claims)

	resp := gin.H{
		"mode":        "platform",
		"id":          userID.String(),
		"email":       claims.Email,
		"isStaff":     claims.IsStaff,
		"isSuperuser": claims.IsSuperuser,
		"groups":      claims.Groups,
		"permissions": perms,
		"user": gin.H{
			"id":          userID.String(),
			"email":       claims.Email,
			"isStaff":     claims.IsStaff,
			"isSuperuser": claims.IsSuperuser,
			"groups":      claims.Groups,
			"permissions": perms,
		},
	}

	if p.Repo != nil {
		_ = p.Repo.Notifications.RegisterRecipient(c.Request.Context(), userID.String(), claims.Email)
		if profile, err := p.Repo.UserProfiles.Get(c.Request.Context(), userID.String()); err == nil {
			resp["profile"] = profile
		}
	}

	c.JSON(http.StatusOK, resp)
}

// profileBody is the editable profile payload for PUT /users/me/profile.
// Every field is optional; omitted fields upsert as empty strings.
type profileBody struct {
	DisplayName  string `json:"displayName"`
	Role         string `json:"role"`
	Department   string `json:"department"`
	Phone        string `json:"phone"`
	ContactEmail string `json:"contactEmail"`
	Bio          string `json:"bio"`
	Avatar       string `json:"avatar"`
}

func (p *Platform) updateProfile(c *gin.Context) {
	if _, ok := auth.PlatformClaimsFromContext(c); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	if p.Repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "profile store unavailable"})
		return
	}

	var body profileBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(body.Avatar) > maxAvatarLen {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "avatar too large"})
		return
	}

	userID, _ := auth.PlatformUserID(c)
	profile, err := p.Repo.UserProfiles.Upsert(c.Request.Context(), userID.String(), store.UserProfileInput{
		DisplayName:  body.DisplayName,
		Role:         body.Role,
		Department:   body.Department,
		Phone:        body.Phone,
		ContactEmail: body.ContactEmail,
		Bio:          body.Bio,
		Avatar:       body.Avatar,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, profile)
}
