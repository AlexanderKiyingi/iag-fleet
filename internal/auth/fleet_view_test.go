package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	platformauth "github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/iag/fleet-tool/backend/internal/ctxkeys"
)

// runFleetView drives RequireAnyFleetView against a context carrying the given
// permissions under strict RBAC, returning the HTTP status written (200 when
// the middleware called Next without aborting).
func runFleetView(t *testing.T, perms []string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(strictRBACKey, true)
	c.Set(ctxkeys.Claims, &platformauth.Claims{
		PrincipalType: platformauth.PrincipalUser,
		Permissions:   perms,
	})
	c.Set(ctxkeys.Permissions, perms)
	c.Set(ctxkeys.UserID, uuid.New())

	// On success RequireAnyPerm calls c.Next(); with no further handlers
	// registered that's a no-op and the context is never aborted.
	RequireAnyFleetView()(c)
	if !c.IsAborted() {
		return http.StatusOK
	}
	return w.Code
}

// A principal holding any fleet view permission may read aggregate endpoints.
func TestRequireAnyFleetViewAllowsHolderOfAnyViewPerm(t *testing.T) {
	if code := runFleetView(t, []string{"fleet.view_fuel_record"}); code != http.StatusOK {
		t.Fatalf("holder of view_fuel_record: status %d, want 200", code)
	}
}

// An authenticated principal with no fleet permissions (e.g. scoped only to
// another domain) is denied — the aggregate endpoints don't leak fleet data.
func TestRequireAnyFleetViewDeniesNonFleetPrincipal(t *testing.T) {
	if code := runFleetView(t, nil); code != http.StatusForbidden {
		t.Fatalf("principal with no fleet perms: status %d, want 403", code)
	}
}
