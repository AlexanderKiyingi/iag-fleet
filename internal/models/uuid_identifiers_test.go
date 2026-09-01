package models

import (
	"reflect"
	"strings"
	"testing"
)

// Row identifiers are uuids throughout. Every column that holds a fleet entity
// id or a reference to one is UUID in the database (0043, and 0046 for the one
// it missed), and the matching model field carries dbcast:"uuid" so the store
// casts it on read and turns "" into NULL on write.
//
// Getting that wrong is not a small bug. When 0043 was applied while the models
// still described those columns as plain strings, buildSelectExpr emitted
// COALESCE(id, ”) against a uuid column and every collection returned 500 —
// the whole service, until each field was tagged.
//
// This walks the models rather than the schema so it runs without a database,
// on every build, in the place a new field is actually added.
//
// The exceptions are listed one by one with the reason. A column belongs here
// only if it genuinely must not be a uuid; "not converted yet" is not a reason,
// it is the bug this test exists to catch.
var notUUID = map[string]string{
	// Not a reference at all — a person's national identity number.
	"Driver.national_id": "government id, free text",

	// Polymorphic: paired with a kind/type column and pointing at any table, so
	// no single mapping is correct.
	"TaskItem.source_id": "polymorphic, paired with source",

	// Owned by another service. Fleet stores what it was given.
	"Part.warehouse_item_id": "id owned by iag-warehouse",

	// A log table keyed by bigint, not a uuid entity.
	"FuelRecord.fuel_event_id": "fuel_events.id is bigint",
}

func TestEntityReferenceColumnsAreUUID(t *testing.T) {
	types := []any{
		Vehicle{}, Driver{}, JMP{}, Cargo{}, CargoDoc{}, FuelRecord{}, FuelRequest{},
		MaintenanceItem{}, Part{}, Tyre{}, Trip{}, SafetyEvent{}, ComplianceItem{},
		ServiceRequest{}, TaskItem{}, DeploymentDay{}, InspectionTemplate{},
		VehicleInspection{}, PMSchedule{},
	}

	checked := 0
	for _, model := range types {
		rt := reflect.TypeOf(model)
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			col := f.Tag.Get("db")
			if col == "" || (col != "id" && !strings.HasSuffix(col, "_id")) {
				continue
			}
			key := rt.Name() + "." + col
			if reason, ok := notUUID[key]; ok {
				if f.Tag.Get("dbcast") == "uuid" {
					t.Errorf("%s is tagged uuid but listed as an exception (%s) — "+
						"remove it from notUUID or drop the tag", key, reason)
				}
				continue
			}
			checked++
			if f.Tag.Get("dbcast") != "uuid" {
				t.Errorf("%s (%s) holds an entity id but is not tagged dbcast:\"uuid\".\n"+
					"Every fleet id column is UUID. Without the tag the store emits "+
					"COALESCE(%s, '') on a uuid column and every read of %s fails.\n"+
					"If this column genuinely must not be a uuid, add it to notUUID with "+
					"the reason.", key, f.Name, col, rt.Name())
			}
		}
	}

	if checked < 50 {
		t.Fatalf("only %d id columns checked; the model list has probably drifted", checked)
	}
}

// The exception list must not rot: an entry naming a field that no longer
// exists hides the fact that nothing is being excluded any more.
func TestNotUUIDExceptionsAllExist(t *testing.T) {
	types := map[string]reflect.Type{}
	for _, model := range []any{Driver{}, TaskItem{}, Part{}, FuelRecord{}} {
		rt := reflect.TypeOf(model)
		types[rt.Name()] = rt
	}
	for key := range notUUID {
		name, col, ok := strings.Cut(key, ".")
		if !ok {
			t.Fatalf("malformed exception key %q, want Model.column", key)
		}
		rt, ok := types[name]
		if !ok {
			t.Fatalf("exception %q names model %s, which this test does not load", key, name)
		}
		found := false
		for i := 0; i < rt.NumField(); i++ {
			if rt.Field(i).Tag.Get("db") == col {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("exception %q names a column %s no longer has — remove it", key, name)
		}
	}
}
