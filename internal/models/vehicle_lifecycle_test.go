package models_test

import (
	"strings"
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// The frontend runs the same machine in src/lib/iag/vehicle-lifecycle.ts and
// renders its transition buttons from that copy. The two repositories cannot
// import each other, so these cases are the contract: they are the same ones
// that file's own tests pin, and a change to either machine means changing both.

func TestToVehicleLifecycleState(t *testing.T) {
	cases := map[string]string{
		"":                  "Active", // every row from before migration 0048
		"   ":               "Active",
		"active":            "Active",
		"ACTIVE":            "Active",
		"Held for disposal": "Held for disposal",
		"nonsense":          "Active", // never silently terminal, never blocking
	}
	for in, want := range cases {
		if got := models.ToVehicleLifecycleState(in); got != want {
			t.Errorf("ToVehicleLifecycleState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVehicleLifecycleAllowedMoves(t *testing.T) {
	ok := [][2]string{
		{"Ordered", "In commissioning"},
		{"In commissioning", "Active"},
		{"Active", "Under maintenance"},
		{"Under maintenance", "Active"},
		{"Grounded", "Active"},
		{"Held for disposal", "Active"}, // reinstatement, right up to the end
	}
	for _, m := range ok {
		if err := models.CheckVehicleLifecycleTransition(m[0], m[1], "a reason", "Sale"); err != "" {
			t.Errorf("%s → %s should be allowed, refused with %q", m[0], m[1], err)
		}
	}
}

func TestVehicleLifecycleRefusesMovesThatAreNotOffered(t *testing.T) {
	if err := models.CheckVehicleLifecycleTransition("Ordered", "Active", "", ""); err == "" {
		t.Error("Ordered → Active is not a permitted move and must be refused")
	}
	if err := models.CheckVehicleLifecycleTransition("Active", "Active", "", ""); err == "" {
		t.Error("a move to the state the vehicle is already in must be refused")
	}
	if err := models.CheckVehicleLifecycleTransition("Active", "Scrapped", "", ""); err == "" {
		t.Error("an unknown target state must be refused")
	}
}

// Disposal is terminal because an accidental reinstatement would contradict the
// ERP asset retirement FR-VEH-10 fires.
func TestDisposedIsTerminal(t *testing.T) {
	for _, to := range models.VehicleLifecycleStates {
		if to == "Disposed" {
			continue
		}
		err := models.CheckVehicleLifecycleTransition("Disposed", to, "a reason", "Sale")
		if err == "" {
			t.Errorf("Disposed → %s must be refused; a disposed asset cannot re-enter the fleet", to)
		}
		if !strings.Contains(err, "final") {
			t.Errorf("Disposed → %s refused with %q, which does not say why", to, err)
		}
	}
}

func TestVehicleLifecycleReasonIsRequiredWhereItMatters(t *testing.T) {
	needsReason := [][2]string{
		{"Active", "Grounded"},
		{"Active", "Held for disposal"},
		{"Held for disposal", "Disposed"},
		{"Held for disposal", "Active"},
	}
	for _, m := range needsReason {
		if err := models.CheckVehicleLifecycleTransition(m[0], m[1], "", "Sale"); err == "" {
			t.Errorf("%s → %s must require a reason", m[0], m[1])
		}
		if err := models.CheckVehicleLifecycleTransition(m[0], m[1], "Accident damage", "Sale"); err != "" {
			t.Errorf("%s → %s with a reason should pass, refused with %q", m[0], m[1], err)
		}
	}

	// Routine moves do not: going in and out of maintenance is not a decision
	// anybody is later asked to justify.
	if err := models.CheckVehicleLifecycleTransition("Active", "Under maintenance", "", ""); err != "" {
		t.Errorf("Active → Under maintenance should not need a reason, refused with %q", err)
	}
}

func TestDisposalRequiresAMethod(t *testing.T) {
	err := models.CheckVehicleLifecycleTransition("Held for disposal", "Disposed", "Uneconomic to repair", "")
	if err == "" {
		t.Fatal("disposal without a method must be refused — it triggers an ERP retirement")
	}
	if !strings.Contains(err, "method") {
		t.Errorf("refusal %q does not tell the caller what is missing", err)
	}
}

// Only a fully Active vehicle is dispatchable. One held for disposal is neither
// `offline` nor `maintenance` on the operational status, so without this it
// would read as available on every dispatch screen.
func TestOnlyActiveIsDispatchable(t *testing.T) {
	for _, s := range models.VehicleLifecycleStates {
		want := s == "Active"
		if got := models.VehicleLifecycleIsDispatchable(s); got != want {
			t.Errorf("dispatchable(%q) = %v, want %v", s, got, want)
		}
	}
	// Unrecorded lifecycle normalises to Active, which is what every row
	// predating 0048 actually is.
	if !models.VehicleLifecycleIsDispatchable("") {
		t.Error("a vehicle with no recorded lifecycle must stay dispatchable")
	}
}

// Every state the machine can reach has to be reachable in the transition map,
// or a vehicle could be moved into a state it can never leave.
func TestEveryStateHasAnEntry(t *testing.T) {
	for _, s := range models.VehicleLifecycleStates {
		if _, ok := models.VehicleLifecycleTransitions[s]; !ok {
			t.Errorf("state %q has no entry in the transition map", s)
		}
	}
	for from, tos := range models.VehicleLifecycleTransitions {
		for _, to := range tos {
			if models.ToVehicleLifecycleState(to) != to {
				t.Errorf("%s → %q names a state that is not in VehicleLifecycleStates", from, to)
			}
		}
	}
}
