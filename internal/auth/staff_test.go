package auth

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	platformauth "github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/iag/fleet-tool/backend/internal/ctxkeys"
)

// ctxFor builds a strict-RBAC context for a principal with no permissions of
// its own, so the only thing that can grant access is a claim short-circuit.
func ctxFor(claims *platformauth.Claims) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(strictRBACKey, true)
	c.Set(ctxkeys.Claims, claims)
	c.Set(ctxkeys.Permissions, []string(nil))
	c.Set(ctxkeys.UserID, uuid.New())
	return c
}

// A staff token is not a master key. It reads; it must not write.
//
// Fleet used to return true for every codename on is_staff alone — including
// delete_vehicle — which no other service in the platform does. The gateway
// masks this on proxied traffic, so only a direct-to-service call would have
// exercised it.
func TestStaffMayReadButNotWrite(t *testing.T) {
	staff := &platformauth.Claims{
		PrincipalType: platformauth.PrincipalUser,
		IsStaff:       true,
	}

	for _, codename := range []string{"view_vehicle", "fleet.view_driver", "view_trip"} {
		if !HasPerm(ctxFor(staff), codename) {
			t.Errorf("staff should retain read access to %s", codename)
		}
	}

	for _, codename := range []string{
		"add_vehicle", "change_vehicle", "delete_vehicle",
		"fleet.delete_driver", "manage_iot_device",
	} {
		if HasPerm(ctxFor(staff), codename) {
			t.Errorf("staff must NOT be granted %s without an explicit permission", codename)
		}
	}
}

// Superuser is the one claim that is authoritative platform-wide — the auth
// service deliberately issues superusers no permission list at all (it breached
// header size limits), so every service short-circuits on this claim.
func TestSuperuserStillBypasses(t *testing.T) {
	su := &platformauth.Claims{
		PrincipalType: platformauth.PrincipalUser,
		IsSuperuser:   true,
	}
	for _, codename := range []string{"view_vehicle", "delete_vehicle"} {
		if !HasPerm(ctxFor(su), codename) {
			t.Errorf("superuser should pass %s", codename)
		}
	}
}

// A staff caller that genuinely holds a mutate permission still gets it — the
// change removes the blanket grant, not the normal path.
func TestStaffWithExplicitPermissionMayWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(strictRBACKey, true)
	c.Set(ctxkeys.Claims, &platformauth.Claims{
		PrincipalType: platformauth.PrincipalUser,
		IsStaff:       true,
		Permissions:   []string{"fleet.change_vehicle"},
	})
	c.Set(ctxkeys.Permissions, []string{"fleet.change_vehicle"})
	c.Set(ctxkeys.UserID, uuid.New())

	if !HasPerm(c, "change_vehicle") {
		t.Error("staff holding fleet.change_vehicle should be allowed to change a vehicle")
	}
	if HasPerm(c, "delete_vehicle") {
		t.Error("that permission must not extend to delete")
	}
}
