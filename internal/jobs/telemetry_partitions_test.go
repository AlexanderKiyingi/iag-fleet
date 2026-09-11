package jobs

import (
	"testing"
	"time"
)

// The bound is parsed out of text because Postgres keeps no parsed form of it.
// Every caller drops a table on the answer, so the failure that matters is a
// bound being misread as older than it is.
func TestPartitionEnd(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bound string
		want  string
	}{
		{
			"timestamptz with numeric offset — what Postgres renders here",
			"FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00')",
			"2026-10-01T00:00:00Z",
		},
		{
			"non-UTC offset is normalised, not taken at face value",
			"FOR VALUES FROM ('2026-08-31 21:00:00-03') TO ('2026-09-30 21:00:00-03')",
			"2026-10-01T00:00:00Z",
		},
		{
			"bare date",
			"FOR VALUES FROM ('2026-09-01') TO ('2026-10-01')",
			"2026-10-01T00:00:00Z",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := partitionEnd(tc.bound)
			if !ok {
				t.Fatalf("not parsed: %s", tc.bound)
			}
			if got.UTC().Format(time.RFC3339) != tc.want {
				t.Fatalf("got %s, want %s", got.UTC().Format(time.RFC3339), tc.want)
			}
		})
	}
}

// Anything unrecognised must be left alone. DEFAULT is the important one: it is
// unbounded, so it can hold rows of any age — including today's — and it is the
// safety net that keeps ingest alive when a month is missing. Dropping it on a
// parse failure would lose recent telemetry and break writes at the same time.
func TestPartitionEnd_refusesWhatItCannotRead(t *testing.T) {
	for _, bound := range []string{
		"DEFAULT",
		"",
		"FOR VALUES IN ('a','b')",
		"FOR VALUES WITH (MODULUS 4, REMAINDER 1)",
		"FOR VALUES FROM ('2026-09-01') TO ('not-a-date')",
	} {
		if _, ok := partitionEnd(bound); ok {
			t.Fatalf("claimed to understand %q — a caller would drop that table", bound)
		}
	}
}

// Retention drops a month only when the whole month is past the cutoff. A
// partition whose end is after the cutoff still holds rows inside the window.
func TestPartitionEnd_cutoffBoundary(t *testing.T) {
	end, ok := partitionEnd("FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00')")
	if !ok {
		t.Fatal("parse failed")
	}
	cutoff := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	// Ends exactly at the cutoff: everything in it is older, so it goes.
	if end.After(cutoff) {
		t.Fatal("a partition ending at the cutoff should be droppable")
	}
	// One second earlier and it still holds retained rows.
	if !end.After(cutoff.Add(-time.Second)) {
		t.Fatal("a partition ending after the cutoff must be kept")
	}
}
