package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/config"
)

func denyIfProduction(c *gin.Context, cfg config.Config, endpoint string) bool {
	if !cfg.IsProduction() {
		return false
	}
	c.JSON(http.StatusForbidden, gin.H{
		"error":    endpoint + " is disabled in production",
		"endpoint": endpoint,
	})
	return true
}
