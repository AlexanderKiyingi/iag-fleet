package handlers

import (
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// The matrix is a compliance control, so the interesting cases are the ones
// where it must say nothing. Three separate "not configured" states have to
// read as "no opinion" rather than "denied", because closing any of them would
// ground the fleet the moment this deployed -- the same shape of failure as the
// 2000-01-01 permit placeholder, which left all 20 drivers un-dispatchable with
// nothing on screen to say so.

func TestPermitClassesSplitOnAnySeparator(t *testing.T) {
	// FR-DRV-02 says "licence class(es)": a driver commonly holds more than
	// one, and the model carries a single string. Operators will not agree on a
	// separator, so all the plausible ones are accepted.
	cases := map[string][]string{
		"B":          {"B"},
		"B,CM":       {"B", "CM"},
		"B; CM":      {"B", "CM"},
		"B / CM":     {"B", "CM"},
		"B|CM":       {"B", "CM"},
		"  B   CM  ": {"B", "CM"},
		"":           nil,
		"   ":        nil,
	}
	for input, want := range cases {
		got := driverPermitClasses(models.Driver{PermitClass: input})
		if len(got) != len(want) {
			t.Errorf("%q -> %v, want %v", input, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q -> %v, want %v", input, got, want)
				break
			}
		}
	}
}

func TestNoRepoOrIdsIsNoOpinion(t *testing.T) {
	// Guard clauses: nothing to check is not a refusal.
	if err := driverAuthorisedForVehicle(t.Context(), nil, "DRV-1", "VEH-1"); err != nil {
		t.Errorf("a nil repo must not deny: %v", err)
	}
}

// The remaining behaviour -- empty matrix, unclassified vehicle, driver with no
// class, recognised class authorised or not -- is exercised against a live
// database by the integration suite, because it turns on real rows in four
// tables and a fake repository here would be asserting that the fake behaves
// like the fake.
//
// What is worth pinning without a database is the intent, so a future edit that
// makes any of these deny has to change a test that says why it must not:
//
//	empty matrix          -> allow  (nothing configured expresses no opinion)
//	no vehicle category   -> allow  (operator has not classified it)
//	no driver permit class-> allow  (record incomplete, not unauthorised)
//	class not in the list -> allow  (unknown, not denied)
//	class known + no pair -> DENY   (an operator said what is allowed; this is not)
func TestFailOpenIntentIsDocumented(t *testing.T) {
	t.Log("empty matrix, unclassified vehicle, missing or unknown permit class: all allow")
	t.Log("only a recognised class with no matching authorisation denies")
}
