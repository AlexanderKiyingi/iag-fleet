package handlers

import (
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// The toolbox talk is a pre-dispatch safety attestation, and the handler used to
// write all eight confirmations regardless of what the driver actually ticked —
// on the assumption that callers had already PATCHed the items, which no caller
// ever did. These pin the mapping that replaced it.
//
// The two sides do not agree on four of the eight names (the UI's `drunkDriving`
// is the model's `noDrunkDriving`, `commsProtocol` is `communication`, `fatigue`
// is `fatigueManagement`, `parking` is `parkingConfirmed`). A silent miss there
// is a check the driver made that the record does not show, so each alias is
// asserted rather than assumed.

func TestToolboxChecksMapFromTheUIIds(t *testing.T) {
	uiIDs := []string{
		"drunkDriving", "speedLimits", "cargoInspection", "commsProtocol",
		"fatigue", "incidentContacts", "routeReviewed", "parking",
	}
	items := toolboxItemsFromChecks(uiIDs)
	if missing := missingToolboxItems(items); len(missing) != 0 {
		t.Fatalf("the eight ids the toolbox UI submits must map to all eight items; missing %v", missing)
	}
}

func TestToolboxChecksAlsoAcceptTheServiceOwnNames(t *testing.T) {
	// A client speaking the model's vocabulary is equally valid.
	serviceNames := []string{
		"noDrunkDriving", "speedLimits", "cargoInspection", "communication",
		"fatigueManagement", "incidentContacts", "routeReviewed", "parkingConfirmed",
	}
	if missing := missingToolboxItems(toolboxItemsFromChecks(serviceNames)); len(missing) != 0 {
		t.Fatalf("service-side names must map too; missing %v", missing)
	}
}

func TestUnconfirmedChecksStayUnconfirmed(t *testing.T) {
	items := toolboxItemsFromChecks([]string{"drunkDriving", "speedLimits"})
	missing := missingToolboxItems(items)
	if len(missing) != 6 {
		t.Fatalf("six items were not ticked and must be reported; got %v", missing)
	}
	// The refusal has to name what is outstanding, in the order the UI shows it.
	if missing[0] != "cargoInspection" || missing[len(missing)-1] != "parking" {
		t.Errorf("missing list is not in UI order: %v", missing)
	}
}

func TestUnknownCheckIdIsIgnoredRatherThanCountedAsConfirmed(t *testing.T) {
	items := toolboxItemsFromChecks([]string{"drunkDriving", "somethingElse"})
	if missing := missingToolboxItems(items); len(missing) != 7 {
		t.Fatalf("an unrecognised id must confirm nothing; missing %v", missing)
	}
}

func TestToolboxCheckListReadsEitherShape(t *testing.T) {
	fromArray := toolboxCheckList(completeToolboxBody{Checks: []string{"a", "b"}})
	if len(fromArray) != 2 {
		t.Errorf("checks array: got %v", fromArray)
	}
	// The app's standalone path stores the ids comma-joined and posts them the
	// same way, so one client can drive both modes.
	fromString := toolboxCheckList(completeToolboxBody{ToolboxChecks: "drunkDriving, speedLimits ,"})
	if len(fromString) != 2 || fromString[1] != "speedLimits" {
		t.Errorf("comma-joined form: got %v", fromString)
	}
	if got := toolboxCheckList(completeToolboxBody{}); got != nil {
		t.Errorf("an empty body names no checks; got %v", got)
	}
}

// A body carrying neither shape falls back to whatever the record already holds,
// which is the only path that can still complete without new evidence — and it
// only completes if the stored items are already complete.
func TestStoredItemsStillCountWhenNoChecksAreSubmitted(t *testing.T) {
	yes := true
	full := models.ToolboxItems{
		NoDrunkDriving: &yes, SpeedLimits: &yes, CargoInspection: &yes,
		Communication: &yes, FatigueManagement: &yes, IncidentContacts: &yes,
		RouteReviewed: &yes, ParkingConfirmed: &yes,
	}
	if missing := missingToolboxItems(full); len(missing) != 0 {
		t.Fatalf("a fully-confirmed stored set must pass; missing %v", missing)
	}
	partial := models.ToolboxItems{NoDrunkDriving: &yes}
	if missing := missingToolboxItems(partial); len(missing) != 7 {
		t.Fatalf("a partially-confirmed stored set must not complete; missing %v", missing)
	}
}
