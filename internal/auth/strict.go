package auth

import "github.com/gin-gonic/gin"

const strictRBACKey = "strict_rbac"

// StrictRBAC enables fail-closed permission checks when JWT permission lists
// are empty (production default).
func StrictRBAC() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(strictRBACKey, true)
		c.Next()
	}
}

func isStrictRBAC(c *gin.Context) bool {
	v, ok := c.Get(strictRBACKey)
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}
