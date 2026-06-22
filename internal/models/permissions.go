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

var crudEntities = []string{
	"vehicle", "driver", "jmp", "cargo", "cargo_doc",
	"maintenance_item", "part", "tyre", "trip",
	"safety_event", "compliance_item", "service_request",
	"task_item", "deployment_day", "fuel_record", "fuel_request",
	"inspection_template", "vehicle_inspection",
}

var workflowPermissions = []PermissionDescriptor{
	{Name: "fleet.approve_mileage_jmp", Description: "Approve JMP mileage claims"},
	{Name: "fleet.approve_fuel_request", Description: "Approve or reject fuel requests"},
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
	{Name: "fleet.view_pm_schedule", Description: "View PM schedules"},
	{Name: "fleet.add_pm_schedule", Description: "Create PM schedules"},
	{Name: "fleet.change_pm_schedule", Description: "Update PM schedules"},
	{Name: "fleet.delete_pm_schedule", Description: "Delete PM schedules"},
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
