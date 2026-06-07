package auth

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	platformauth "github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/iag/fleet-tool/backend/internal/ctxkeys"
)

func TestHasPermStrictRBACDeniesEmptyPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(strictRBACKey, true)
	c.Set(ctxkeys.Claims, &platformauth.Claims{
		PrincipalType: platformauth.PrincipalUser,
		Permissions:   nil,
	})
	c.Set(ctxkeys.UserID, uuid.New())

	if HasPerm(c, "view_vehicle") {
		t.Fatal("strict RBAC should deny when permissions list is empty")
	}
}

func TestHasPermDevAllowsEmptyPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(ctxkeys.Claims, &platformauth.Claims{
		PrincipalType: platformauth.PrincipalUser,
		Permissions:   nil,
	})
	c.Set(ctxkeys.UserID, uuid.New())

	if !HasPerm(c, "view_vehicle") {
		t.Fatal("non-strict mode should allow empty permissions for local dev tokens")
	}
}
