package models

import (
	"fmt"
	"strings"
)

// PermissionDescriptor is posted to iag-authentication at startup.
type PermissionDescriptor struct {
	Name        string
	Description string
}

// crudEntities drive the generic view/add/change/delete permission set. Every
// entity registered as a CRUD Resource (handlers.Resource.Entity) MUST appear
// here, otherwise its routes enforce a permission that is never seeded and the
// routes 403 permanently. handlers.Resource.Register fail-fasts on a mismatch,
// and TestCRUDEntitiesYieldFullVerbSet guards the catalogue side.
var crudEntities = []string{
	"vehicle", "driver", "jmp", "cargo", "cargo_doc",
	"maintenance_item", "part", "tyre", "trip",
	"safety_event", "compliance_item", "service_request",
	"task_item", "deployment_day", "fuel_record", "fuel_request",
	"inspection_template", "vehicle_inspection", "pm_schedule",
}

var workflowPermissions = []PermissionDescriptor{
	{Name: "fleet.approve_mileage_jmp", Description: "Approve JMP mileage claims"},
	{Name: "fleet.approve_fuel_request", Description: "Approve or reject fuel requests"},
	{Name: "fleet.approve_service_request", Description: "Approve or reject service requests"},
	{Name: "fleet.approve_assignment", Description: "Approve the vehicle/driver assignment"},
	{Name: "fleet.approve_jmp", Description: "Approve a journey plan for dispatch"},
	{Name: "fleet.approve_deployment", Description: "Approve deployment / release of a vehicle"},
	{Name: "fleet.override_gate_order", Description: "Bypass status-ordering gates (out-of-order dispatch/JMP transitions; audit-logged)"},
	{Name: "fleet.complete_toolbox_jmp", Description: "Complete toolbox talk on JMP"},
	{Name: "fleet.complete_jmp", Description: "Mark an active JMP as completed"},
	{Name: "fleet.cancel_jmp", Description: "Cancel a JMP"},
	{Name: "fleet.advance_stage_cargo", Description: "Advance cargo workflow stage"},
	{Name: "fleet.offload_cargo", Description: "Offload cargo"},
	{Name: "fleet.demobilise_cargo", Description: "Demobilise cargo"},
	{Name: "fleet.assign_request", Description: "Assign service requests"},
	{Name: "fleet.complete_task", Description: "Complete fleet task items"},
	{Name: "fleet.seed_deployment", Description: "Seed deployment day data"},
	{Name: "fleet.add_deployment_entry", Description: "Add deployment entries"},
	{Name: "fleet.simulate_vehicles", Description: "Run vehicle simulation tick"},
	{Name: "fleet.view_telemetry", Description: "View vehicle telemetry"},
	{Name: "fleet.manage_iot_device", Description: "Manage IoT devices"},
	{Name: "fleet.view_notification", Description: "View in-app notifications"},
	{Name: "fleet.change_notification", Description: "Update notification state"},
	// pm_schedule view/add/change/delete come from the generic crudEntities set.
	{Name: "fleet.view_operator_ticker", Description: "View operator ticker"},
	{Name: "fleet.change_operator_ticker", Description: "Update operator ticker"},
	{Name: "fleet.view_audit_entry", Description: "View fleet audit log"},
	{Name: "fleet.export_data", Description: "Export fleet data"},
	{Name: "fleet.import_data", Description: "Import fleet data"},
	{Name: "fleet.reset_data", Description: "Reset fleet demo data"},
}

// PermissionDescriptors returns fleet.* codenames for central auth registration.
// API handlers accept unprefixed legacy aliases (view_vehicle ↔ fleet.view_vehicle).
func PermissionDescriptors() []PermissionDescriptor {
	out := make([]PermissionDescriptor, 0, len(crudEntities)*4+len(workflowPermissions))
	for _, entity := range crudEntities {
		label := strings.ReplaceAll(entity, "_", " ")
		for _, pair := range []struct{ act, verb string }{
			{"view", "View"},
			{"add", "Add"},
			{"change", "Change"},
			{"delete", "Delete"},
		} {
			out = append(out, PermissionDescriptor{
				Name:        fmt.Sprintf("fleet.%s_%s", pair.act, entity),
				Description: fmt.Sprintf("%s %s", pair.verb, label),
			})
		}
	}
	out = append(out, workflowPermissions...)
	return out
}

// CRUDEntities returns the entities that get a generic view/add/change/delete
// permission set. Kept as the single source of truth for CRUD route entities.
func CRUDEntities() []string {
	return append([]string(nil), crudEntities...)
}

// IsCatalogued reports whether a permission codename is in the registered
// catalogue, accepting either the prefixed form ("fleet.view_vehicle") or the
// legacy unprefixed alias ("view_vehicle") that API handlers enforce.
func IsCatalogued(codename string) bool {
	name := codename
	if !strings.HasPrefix(name, "fleet.") {
		name = "fleet." + name
	}
	for _, d := range PermissionDescriptors() {
		if d.Name == name {
			return true
		}
	}
	return false
}
