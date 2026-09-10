package store

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// A nonsense id used to answer "not found". Migration 0043 retyped ids to uuid,
// so Postgres now rejects it before it can match anything — and that error
// reached the client as a 500 carrying raw database text.
func TestIdLookupErr_malformedIDIsNotFound(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:    "22P02",
		Message: `invalid input syntax for type uuid: "VEH-NOPE"`,
	}
	if got := idLookupErr(pgErr); !errors.Is(got, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", got)
	}
}

func TestIdLookupErr_wrappedMalformedIDIsNotFound(t *testing.T) {
	wrapped := errors.Join(errors.New("query failed"), &pgconn.PgError{Code: "22P02"})
	if got := idLookupErr(wrapped); !errors.Is(got, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", got)
	}
}

// Everything else has to pass through untouched. Reporting a connection
// failure as "not found" would turn an outage into a silent empty result,
// which is far worse than the 500 this fix removes.
func TestIdLookupErr_otherErrorsPassThrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"connection failure", errors.New("connection refused")},
		{"unique violation", &pgconn.PgError{Code: "23505"}},
		{"undefined column", &pgconn.PgError{Code: "42703"}},
		{"foreign key violation", &pgconn.PgError{Code: "23503"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := idLookupErr(tc.err)
			if errors.Is(got, ErrNotFound) {
				t.Fatalf("%v must not be reported as not-found", tc.err)
			}
			if !errors.Is(got, tc.err) {
				t.Fatalf("got %v, want the original error", got)
			}
		})
	}
}

func TestIdLookupErr_nilStaysNil(t *testing.T) {
	if got := idLookupErr(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}
