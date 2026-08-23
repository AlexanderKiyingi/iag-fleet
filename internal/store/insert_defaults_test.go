package store

import (
	"strings"
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// The generic insert named every column, which is right for a full row and
// wrong for a partial one: naming a column overrides its DEFAULT, so a nil
// slice or an empty temporal string wrote NULL and the column's own DEFAULT
// never applied. On a NOT NULL column that surfaced as a raw Postgres
// null-violation, and creating the record through the API was impossible.
//
// It was hit on seven columns across six tables before anyone traced it to one
// cause. These pin the fix so it does not regress into another six.

func planFor[T any, PT IdentifiablePtr[T]](t *testing.T, item T) (string, []any) {
	t.Helper()
	c := NewCollection[T, PT](nil, "t")
	cols, _, args, err := c.insertPlan(item)
	if err != nil {
		t.Fatalf("insertPlan: %v", err)
	}
	return cols, args
}

func TestZeroValuedDefaultColumnIsOmitted(t *testing.T) {
	// created_at is TIMESTAMPTZ NOT NULL DEFAULT NOW(). Left empty, the column
	// must not appear at all, so Postgres fills it.
	cols, _ := planFor[models.TaskItem, *models.TaskItem](t, models.TaskItem{
		ID: "TSK-1", Title: "no created_at supplied",
	})
	if strings.Contains(cols, "created_at") {
		t.Errorf("created_at must be omitted when empty so the default applies; got %q", cols)
	}
	if !strings.Contains(cols, "title") {
		t.Errorf("ordinary columns must still be inserted; got %q", cols)
	}
}

func TestSuppliedDefaultColumnIsKept(t *testing.T) {
	// A caller that does supply the value still gets it written - the point is
	// to defer to the database, not to ignore the caller.
	cols, args := planFor[models.TaskItem, *models.TaskItem](t, models.TaskItem{
		ID: "TSK-2", Title: "explicit", CreatedAt: "2026-08-23T00:00:00Z",
	})
	if !strings.Contains(cols, "created_at") {
		t.Fatalf("an explicitly supplied created_at must be inserted; got %q", cols)
	}
	found := false
	for _, a := range args {
		if s, ok := a.(string); ok && s == "2026-08-23T00:00:00Z" {
			found = true
		}
	}
	if !found {
		t.Error("the supplied timestamp did not reach the args")
	}
}

func TestNilSliceDefaultsButEmptySliceIsSent(t *testing.T) {
	// jmps.parking_photos is TEXT[] NOT NULL DEFAULT '{}'. A nil slice means
	// "not supplied" and must defer to the default; an explicitly empty slice
	// is a real value and must be written.
	cols, _ := planFor[models.JMP, *models.JMP](t, models.JMP{ID: "JMP-1"})
	if strings.Contains(cols, "parking_photos") {
		t.Errorf("a nil slice must defer to the column default; got %q", cols)
	}

	cols, _ = planFor[models.JMP, *models.JMP](t, models.JMP{
		ID: "JMP-2", ParkingPhotos: []string{},
	})
	if !strings.Contains(cols, "parking_photos") {
		t.Errorf("an explicitly empty slice is a value and must be sent; got %q", cols)
	}
}

func TestPlaceholdersStayContiguous(t *testing.T) {
	// Omitting a column mid-list must renumber the placeholders, or every
	// argument after the gap binds to the wrong column - which would be far
	// worse than the bug being fixed.
	c := NewCollection[models.TaskItem, *models.TaskItem](nil, "task_items")
	cols, params, args, err := c.insertPlan(models.TaskItem{ID: "TSK-3", Title: "x"})
	if err != nil {
		t.Fatalf("insertPlan: %v", err)
	}
	nCols := len(strings.Split(cols, ", "))
	nParams := len(strings.Split(params, ", "))
	if nCols != nParams || nParams != len(args) {
		t.Fatalf("cols=%d params=%d args=%d must all match", nCols, nParams, len(args))
	}
	for i := range args {
		want := "$" + itoa(i+1)
		if !strings.Contains(params, want) {
			t.Errorf("placeholder %s missing from %q", want, params)
		}
	}
}

func TestCollectionWithoutDefaultsIsUnchanged(t *testing.T) {
	// Drivers tag nothing, so they must keep the precomputed full-column path.
	// This is what keeps the change from touching every other entity.
	c := NewCollection[models.Driver, *models.Driver](nil, "drivers")
	if c.anyDefaults {
		t.Fatal("drivers has no dbdefault column and must not take the per-row path")
	}
	cols, params, _, err := c.insertPlan(models.Driver{ID: "DRV-1", Name: "x"})
	if err != nil {
		t.Fatalf("insertPlan: %v", err)
	}
	if cols != c.insertCols || params != c.insertParams {
		t.Error("a collection without defaults must reuse the precomputed statement")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
