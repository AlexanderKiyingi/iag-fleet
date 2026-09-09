package handlers

import (
	"encoding/json"
	"testing"
)

// ServerOwnedFields is what stops a record form walking around a state machine.
//
// The case it exists for: a maintenance work order becomes `completed` by
// POST /:id/complete, which is the call that draws the parts. A PATCH that set
// `status` directly skipped the draw — and because completion refuses an
// already-completed row, the work order was then stranded: completed on screen,
// no stock moved, and no way to complete it properly.

var owned = map[string]string{
	"lifecycleState": "POST /api/vehicles/:id/lifecycle",
	"disposalDate":   "POST /api/vehicles/:id/lifecycle",
}

func TestRejectServerOwnedNamesTheOffendingField(t *testing.T) {
	field, owner := rejectServerOwned([]byte(`{"plate":"UAA 123A","lifecycleState":"Disposed"}`), owned)
	if field != "lifecycleState" {
		t.Fatalf("field = %q, want lifecycleState", field)
	}
	if owner == "" {
		t.Error("the refusal must say which endpoint owns the field")
	}
}

func TestRejectServerOwnedAllowsAnOrdinaryPatch(t *testing.T) {
	if field, _ := rejectServerOwned([]byte(`{"plate":"UAA 123A","notes":"x"}`), owned); field != "" {
		t.Errorf("an ordinary patch must pass; got %q", field)
	}
}

// A field present but null is still the caller claiming it: `{"lifecycleState":
// null}` would clear the column on merge, which is exactly the write being
// prevented.
func TestRejectServerOwnedCatchesAnExplicitNull(t *testing.T) {
	if field, _ := rejectServerOwned([]byte(`{"lifecycleState":null}`), owned); field != "lifecycleState" {
		t.Error("an explicit null still names the field and must be refused")
	}
}

// Go map iteration order is random. A body naming two owned fields must report
// the same one every time, or the error message changes between identical
// requests.
func TestRejectServerOwnedIsDeterministic(t *testing.T) {
	body := []byte(`{"lifecycleState":"Disposed","disposalDate":"2026-01-01"}`)
	first, _ := rejectServerOwned(body, owned)
	for i := 0; i < 50; i++ {
		if got, _ := rejectServerOwned(body, owned); got != first {
			t.Fatalf("reported %q then %q for the same body", first, got)
		}
	}
}

func TestRejectServerOwnedIsInertWithNoConfiguration(t *testing.T) {
	if field, _ := rejectServerOwned([]byte(`{"lifecycleState":"Disposed"}`), nil); field != "" {
		t.Error("a resource with no server-owned fields must not refuse anything")
	}
}

type ownedRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Locked   string `json:"locked,omitempty"`
	Untagged string `json:"untagged,omitempty"`
}

// PUT replaces the whole object, so a client echoing back what it read is not
// making a claim about these fields. They are carried forward instead of
// refused — otherwise every full-replace client would have to strip them.
func TestPreserveServerOwnedCarriesTheStoredValueForward(t *testing.T) {
	stored := ownedRow{ID: "1", Name: "old", Locked: "kept"}
	incoming := ownedRow{ID: "1", Name: "new", Locked: "attempted"}

	if err := preserveServerOwned(&incoming, stored, map[string]string{"locked": "somewhere else"}); err != nil {
		t.Fatalf("preserveServerOwned: %v", err)
	}
	if incoming.Locked != "kept" {
		t.Errorf("Locked = %q, want the stored value %q", incoming.Locked, "kept")
	}
	if incoming.Name != "new" {
		t.Errorf("Name = %q — a field the caller does own must still be replaced", incoming.Name)
	}
}

// `omitempty` drops an unset field from the stored row entirely, so there is
// nothing to copy. That has to restore the zero value, not leave the caller's.
func TestPreserveServerOwnedClearsWhatUpstreamDoesNotHave(t *testing.T) {
	stored := ownedRow{ID: "1", Name: "old"} // Locked is unset and omitted
	incoming := ownedRow{ID: "1", Name: "new", Locked: "attempted"}

	if err := preserveServerOwned(&incoming, stored, map[string]string{"locked": "somewhere else"}); err != nil {
		t.Fatalf("preserveServerOwned: %v", err)
	}
	if incoming.Locked != "" {
		t.Errorf("Locked = %q, want empty — upstream has no value to carry forward", incoming.Locked)
	}
}

func TestPreserveServerOwnedIsInertWithNoConfiguration(t *testing.T) {
	incoming := ownedRow{ID: "1", Locked: "attempted"}
	if err := preserveServerOwned(&incoming, ownedRow{ID: "1", Locked: "kept"}, nil); err != nil {
		t.Fatalf("preserveServerOwned: %v", err)
	}
	if incoming.Locked != "attempted" {
		t.Error("with nothing configured the body must be left alone")
	}
}

// Round-trip sanity: the helper marshals and unmarshals, so a field it does not
// touch must survive unchanged.
func TestPreserveServerOwnedDoesNotDisturbOtherFields(t *testing.T) {
	incoming := ownedRow{ID: "1", Name: "new", Untagged: "carried"}
	before, _ := json.Marshal(incoming)
	if err := preserveServerOwned(&incoming, ownedRow{ID: "1"}, map[string]string{"locked": "x"}); err != nil {
		t.Fatalf("preserveServerOwned: %v", err)
	}
	after, _ := json.Marshal(incoming)
	if string(before) != string(after) {
		t.Errorf("untouched fields changed:\n before %s\n after  %s", before, after)
	}
}
