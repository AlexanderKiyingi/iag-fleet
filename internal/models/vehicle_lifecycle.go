// Vehicle asset lifecycle — the state machine behind FR-VEH-06.
//
// Kept out of models.go, which is type declarations only.
package models

import (
	"fmt"
	"strings"
)

// This machine is a mirror of the frontend's src/lib/iag/vehicle-lifecycle.ts,
// deliberately state for state and reason for reason. The app renders its
// transition buttons from its own copy and this service is what enforces them,
// so a divergence shows up as a button that 409s — which reads as a broken app
// rather than as a disagreement.
//
// The two live in different repositories, so nothing can assert the mirror
// automatically. TestVehicleLifecycleMachine below pins this side against the
// same cases the frontend's vehicle-lifecycle.test.ts pins that side against;
// changing either machine means changing both files.

// DefaultVehicleLifecycleState is where a vehicle with no recorded lifecycle
// sits. Almost every existing row: the columns arrived in 0048.
const DefaultVehicleLifecycleState = "Active"

// VehicleLifecycleStates are the dispositions an asset can hold, in the order
// the frontend lists them.
var VehicleLifecycleStates = []string{
	"Ordered",
	"In commissioning",
	"Active",
	"Under maintenance",
	"Grounded",
	"Held for disposal",
	"Disposed",
}

// VehicleLifecycleTransitions is not a straight line: a vehicle goes in and out
// of maintenance repeatedly, can be grounded from any working state, and can be
// reinstated from "Held for disposal" right up until it is actually disposed of.
// What it cannot do is come back from Disposed — the asset is gone, and an
// accidental reinstatement would contradict the ERP retirement FR-VEH-10 fires.
var VehicleLifecycleTransitions = map[string][]string{
	"Ordered":           {"In commissioning", "Disposed"},
	"In commissioning":  {"Active", "Grounded", "Held for disposal"},
	"Active":            {"Under maintenance", "Grounded", "Held for disposal"},
	"Under maintenance": {"Active", "Grounded", "Held for disposal"},
	"Grounded":          {"Active", "Under maintenance", "Held for disposal"},
	"Held for disposal": {"Disposed", "Active"},
	"Disposed":          {},
}

// vehicleLifecycleReasonRequired keys are "from>to". The test is not "is this
// bad news" but "would somebody later ask why": grounding, marking for
// disposal, disposing, and reinstating something that was going to be scrapped
// are all decisions an audit asks about. Routine maintenance moves are not.
var vehicleLifecycleReasonRequired = map[string]bool{
	"Active>Grounded":                     true,
	"Under maintenance>Grounded":          true,
	"In commissioning>Grounded":           true,
	"Active>Held for disposal":            true,
	"Under maintenance>Held for disposal": true,
	"Grounded>Held for disposal":          true,
	"In commissioning>Held for disposal":  true,
	"Held for disposal>Disposed":          true,
	"Ordered>Disposed":                    true,
	"Held for disposal>Active":            true,
}

// ToVehicleLifecycleState normalises stored text to a known state, matching
// case-insensitively. Anything unrecognised — including the empty string every
// pre-0048 row carries — reads as the default rather than blocking every
// transition or silently becoming terminal.
func ToVehicleLifecycleState(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return DefaultVehicleLifecycleState
	}
	for _, s := range VehicleLifecycleStates {
		if strings.EqualFold(s, raw) {
			return s
		}
	}
	return DefaultVehicleLifecycleState
}

// VehicleLifecycleNextStates returns the states reachable from `from`.
func VehicleLifecycleNextStates(from string) []string {
	return VehicleLifecycleTransitions[ToVehicleLifecycleState(from)]
}

// VehicleLifecycleReasonRequired reports whether this move must carry a reason.
func VehicleLifecycleReasonRequired(from, to string) bool {
	return vehicleLifecycleReasonRequired[ToVehicleLifecycleState(from)+">"+strings.TrimSpace(to)]
}

// VehicleLifecycleIsDispatchable reports whether a vehicle in this state may be
// dispatched. Only a fully Active asset may: one held for disposal is neither
// `offline` nor `maintenance` on the operational status, so without this check
// it would read as dispatchable.
func VehicleLifecycleIsDispatchable(state string) bool {
	return ToVehicleLifecycleState(state) == "Active"
}

// VehicleDisposalMethods are the ways an asset leaves the fleet (FR-VEH-10).
var VehicleDisposalMethods = []string{"Sale", "Auction", "Write-off", "Trade-in"}

// CheckVehicleLifecycleTransition validates a proposed move and returns the
// sentence a person should see, or "" when the move is allowed. A message
// rather than a code because every caller so far wants exactly that.
func CheckVehicleLifecycleTransition(from, to, reason, disposalMethod string) string {
	current := ToVehicleLifecycleState(from)
	target := strings.TrimSpace(to)

	known := false
	for _, s := range VehicleLifecycleStates {
		if s == target {
			known = true
			break
		}
	}
	if !known {
		if target == "" {
			target = "(nothing)"
		}
		return fmt.Sprintf("%q is not a lifecycle state.", target)
	}
	if target == current {
		return fmt.Sprintf("This vehicle is already %s.", current)
	}
	allowed := VehicleLifecycleTransitions[current]
	if len(allowed) == 0 {
		return fmt.Sprintf("%s is final — a disposed asset cannot re-enter the fleet.", current)
	}
	permitted := false
	for _, s := range allowed {
		if s == target {
			permitted = true
			break
		}
	}
	if !permitted {
		return fmt.Sprintf("Cannot go from %s to %s. Allowed: %s.",
			current, target, strings.Join(allowed, ", "))
	}
	if VehicleLifecycleReasonRequired(current, target) && strings.TrimSpace(reason) == "" {
		return fmt.Sprintf("Moving to %s needs a reason.", target)
	}
	if target == "Disposed" && strings.TrimSpace(disposalMethod) == "" {
		return "Disposal needs a method (sale, auction, write-off or trade-in)."
	}
	return ""
}
