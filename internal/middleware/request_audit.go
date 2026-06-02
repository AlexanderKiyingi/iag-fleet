package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/store"
)

func RequestAudit(repo *store.Repository) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		if isPublicProbePath(c.Request.URL.Path) {
			return
		}

		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		duration := int(time.Since(start).Milliseconds())
		_ = repo.LogAPIRequest(
			c.Request.Context(),
			c.Request.Method,
			path,
			c.Writer.Status(),
			auth.OperatorName(c),
			duration,
			c.ClientIP(),
		)
	}
}
