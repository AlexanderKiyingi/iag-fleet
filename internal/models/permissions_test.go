package models

import "testing"

// The permission catalogue is what gets registered with iag-authentication; a
// codename that handlers enforce but the catalogue omits can never be granted,
// so these guard the catalogue's internal consistency. Route↔catalogue
// alignment is enforced separately at startup by handlers.Resource.Register.

func TestPermissionCatalogueNoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for _, d := range PermissionDescriptors() {
		if seen[d.Name] {
			t.Errorf("duplicate permission codename in catalogue: %s", d.Name)
		}
		seen[d.Name] = true
	}
}

func TestCRUDEntitiesYieldFullVerbSet(t *testing.T) {
	set := make(map[string]struct{})
	for _, d := range PermissionDescriptors() {
		set[d.Name] = struct{}{}
	}
	for _, entity := range CRUDEntities() {
		for _, verb := range []string{"view", "add", "change", "delete"} {
			name := "fleet." + verb + "_" + entity
			if _, ok := set[name]; !ok {
				t.Errorf("CRUD entity %q is missing catalogue permission %q", entity, name)
			}
		}
	}
}

func TestIsCatalogued(t *testing.T) {
	cases := []struct {
		codename string
		want     bool
	}{
		{"fleet.view_vehicle", true},  // prefixed CRUD
		{"view_vehicle", true},        // legacy unprefixed alias
		{"fleet.view_pm_schedule", true}, // moved into crudEntities
		{"add_pm_schedule", true},
		{"fleet.approve_jmp", true},   // workflow permission
		{"override_gate_order", true}, // workflow permission, unprefixed
		{"view_nonexistent_entity", false},
		{"fleet.totally_made_up", false},
	}
	for _, tc := range cases {
		if got := IsCatalogued(tc.codename); got != tc.want {
			t.Errorf("IsCatalogued(%q) = %v, want %v", tc.codename, got, tc.want)
		}
	}
}
