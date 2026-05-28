package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/platform"
)

// PlatformStatus exposes shared-service integration health for operators.
type PlatformStatus struct {
	Services platform.Services
}

func (p *PlatformStatus) Register(rg *gin.RouterGroup) {
	rg.GET("/platform/status", auth.RequireStaff(), p.status)
}

func (p *PlatformStatus) status(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	deps := p.Services.CheckReady(ctx)
	allOK := true
	for _, d := range deps {
		if !d.Skipped && !d.OK {
			allOK = false
			break
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"publicApiUrl":      p.Services.PublicAPIURL,
		"gatewayApiPrefix":  p.Services.GatewayAPIPrefix,
		"authenticationUrl": p.Services.AuthenticationURL,
		"notificationsUrl":  p.Services.NotificationsURL,
		"accountsUrl":       p.Services.AccountsURL,
		"dependenciesOk":    allOK,
		"dependencies":      deps,
	})
}
