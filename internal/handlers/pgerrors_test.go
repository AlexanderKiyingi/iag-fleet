package handlers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// respondError's fallback answered 500 with err.Error(), and the web app shows
// the service's error verbatim — so an operator who left a required box blank
// was shown `null value in column "permit_expiry" of relation "drivers"
// violates not-null constraint (SQLSTATE 23502)`.
//
// Eighteen tables carry a NOT NULL date/uuid column with no default, and the
// store writes NULL for an empty string there, so any of them could produce it.
// These pin the translation that covers all of them.
func TestBadRequestFromPg(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *pgconn.PgError
		want string
	}{
		{
			name: "not null names the field in the caller's own spelling",
			err:  &pgconn.PgError{Code: "23502", TableName: "drivers", ColumnName: "permit_expiry"},
			want: "permitExpiry is required.",
		},
		{
			name: "single-word column needs no conversion",
			err:  &pgconn.PgError{Code: "23502", TableName: "trips", ColumnName: "date"},
			want: "date is required.",
		},
		{
			name: "check constraint yields its column from the default naming",
			err:  &pgconn.PgError{Code: "23514", TableName: "inspection_templates", ConstraintName: "inspection_templates_kind_check"},
			want: "kind is not one of the values this field accepts.",
		},
		{
			name: "cast failure says what kind of problem it is",
			err:  &pgconn.PgError{Code: "22P02"},
			want: "A field is not in the format this record expects.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := badRequestFromPg(tc.err)
			if !ok {
				t.Fatalf("not recognised as a bad request: %+v", tc.err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The message must never carry SQLSTATE, a relation name, or driver text — that
// leakage is the whole reason this exists.
func TestBadRequestFromPg_saysNothingAboutTheDatabase(t *testing.T) {
	msg, ok := badRequestFromPg(&pgconn.PgError{
		Code: "23502", TableName: "drivers", ColumnName: "permit_expiry",
		Message: `null value in column "permit_expiry" of relation "drivers" violates not-null constraint`,
	})
	if !ok {
		t.Fatal("expected a translated message")
	}
	for _, leak := range []string{"SQLSTATE", "relation", "null value", "constraint", "drivers"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("message leaks %q: %s", leak, msg)
		}
	}
}

// Codes that are genuinely the server's problem must keep their 500 — silently
// reporting a connection failure as a bad request would be its own lie.
func TestBadRequestFromPg_leavesServerErrorsAlone(t *testing.T) {
	for _, code := range []string{"23505", "08006", "42P01", "40001", "57014"} {
		if _, ok := badRequestFromPg(&pgconn.PgError{Code: code}); ok {
			t.Fatalf("code %s should not be reported as a bad request", code)
		}
	}
	if _, ok := badRequestFromPg(fmt.Errorf("boom")); ok {
		t.Fatal("a non-Postgres error should not be reported as a bad request")
	}
}

// A constraint that does not follow <table>_<column>_check must not have a
// field name invented for it.
func TestConstraintField_unconventionalNames(t *testing.T) {
	if got := constraintField("vehicles", "some_business_rule"); got != "" {
		t.Fatalf("expected no field for a non-_check constraint, got %q", got)
	}
	if got := constraintField("tyres", "tyres_check"); got != "" {
		t.Fatalf("expected no field when nothing remains after the table, got %q", got)
	}
	if got := constraintField("vehicles", "vehicles_lifecycle_state_check"); got != "lifecycleState" {
		t.Fatalf("got %q, want lifecycleState", got)
	}
	// No table reported: keep whatever the constraint names rather than nothing.
	if got := constraintField("", "kind_check"); got != "kind" {
		t.Fatalf("got %q, want kind", got)
	}
}
