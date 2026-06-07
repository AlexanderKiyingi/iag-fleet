package auth

import (
	"slices"
	"strings"

	"github.com/alvor-technologies/iag-platform-go/authclient"
)

const platformGroupAdmin = "admin"

// platformAdminViewPermissions mirrors gateway fleetViewPermissions for IAM admins.
var platformAdminViewPermissions = []string{
	"fleet.view_vehicle", "fleet.view_driver", "fleet.view_jmp", "fleet.view_cargo",
	"fleet.view_cargo_doc", "fleet.view_maintenance_item", "fleet.view_part",
	"fleet.view_tyre", "fleet.view_trip", "fleet.view_safety_event",
	"fleet.view_compliance_item", "fleet.view_service_request", "fleet.view_task_item",
	"fleet.view_deployment_day", "fleet.view_fuel_record",
	"fleet.view_audit_entry", "fleet.view_operator_ticker",
	"fleet.view_telemetry", "fleet.view_notification", "fleet.view_pm_schedule",
	"fleet.view_inspection_template", "fleet.view_vehicle_inspection",
}

func hasPlatformAdminGroup(claims *authclient.Claims) bool {
	if claims == nil {
		return false
	}
	return slices.Contains(claims.Groups, platformGroupAdmin)
}

func isFleetViewCodename(codename string) bool {
	if strings.HasPrefix(codename, "fleet.view_") {
		return true
	}
	return strings.HasPrefix(codename, "view_")
}

// platformAdminMayView grants read-only fleet access to platform IAM admins.
func platformAdminMayView(claims *authclient.Claims, codename string) bool {
	return hasPlatformAdminGroup(claims) && isFleetViewCodename(codename)
}

// EffectivePermissions returns JWT permissions, augmented for admin-group readers.
func EffectivePermissions(claims *authclient.Claims) []string {
	if claims == nil {
		return nil
	}
	if claims.IsSuperuser {
		return claims.Permissions
	}
	if !hasPlatformAdminGroup(claims) {
		return claims.Permissions
	}
	seen := make(map[string]struct{}, len(claims.Permissions)+len(platformAdminViewPermissions))
	out := append([]string(nil), claims.Permissions...)
	for _, p := range out {
		seen[p] = struct{}{}
	}
	for _, p := range platformAdminViewPermissions {
		if _, ok := seen[p]; !ok {
			out = append(out, p)
			seen[p] = struct{}{}
		}
	}
	return out
}
