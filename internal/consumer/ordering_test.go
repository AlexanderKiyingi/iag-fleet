package consumer

import (
	"os"
	"strings"
	"testing"
)

// This consumer used to write its dedupe row before running the handler. That
// records a failure as success: the message is left uncommitted for redelivery,
// and the redelivery then finds the row and skips the work.
//
// Here the cost was a stale projection rather than lost data — the refresh is an
// absolute SET from the warehouse's own on-hand, and a periodic full reconcile
// backstops it — but the ordering is simply the wrong way round. Applying twice
// is free; skipping is not.
//
// The property is an ordering in SQL and process flow, which no unit test can
// exercise without a database and a broker, so it is pinned at the source.

func TestEventIsMarkedOnlyAfterItHasBeenApplied(t *testing.T) {
	raw, err := os.ReadFile("consumer.go")
	if err != nil {
		t.Fatalf("reading consumer source: %v", err)
	}
	src := string(raw)

	apply := strings.Index(src, "c.apply(ctx, env)")
	if apply < 0 {
		t.Fatal("the handler call moved; this guard needs updating with it")
	}
	mark := strings.Index(src, "INSERT INTO warehouse_event_dedupe")
	if mark < 0 {
		t.Fatal("the dedupe write moved; this guard needs updating with it")
	}
	if mark < apply {
		t.Error("the event is marked before it is applied; a failed refresh would be skipped on redelivery")
	}

	// The check that precedes the work must be a read. An INSERT ... ON CONFLICT
	// used as the check is how the original bug was written: it claims the event
	// as a side effect of asking whether it was handled.
	if !strings.Contains(src, "SELECT EXISTS (SELECT 1 FROM warehouse_event_dedupe") {
		t.Error("the already-handled check is not a plain read; it must not claim the event")
	}
}
