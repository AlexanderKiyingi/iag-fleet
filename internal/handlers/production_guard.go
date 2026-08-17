package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/config"
)

// denyIfProduction blocks demo-only endpoints on any hardened runtime, not just
// one with ENVIRONMENT=production set — see config.HardenedRuntime for why that
// distinction let these stay reachable on Railway.
func denyIfProduction(c *gin.Context, cfg config.Config, endpoint string) bool {
	if !cfg.HardenedRuntime() {
		return false
	}
	c.JSON(http.StatusForbidden, gin.H{
		"error":    endpoint + " is disabled in production",
		"endpoint": endpoint,
	})
	return true
}
