package outbox

import (
	"context"
	"testing"

	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// The point of EnqueueTx is that the outbox row shares the caller's
// transaction. These tests pin both halves of that: it vanishes on rollback,
// and it survives commit. The third case documents why the method exists at
// all — the pooled Enqueue does neither.

func TestEnqueueTx_rollsBackWithTheDomainWrite(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()
	ctx := context.Background()
	s := NewStore(pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.EnqueueTx(ctx, tx, "fleet.maintenance.completed", "WO-1", map[string]string{"maintenanceId": "WO-1"}); err != nil {
		t.Fatalf("EnqueueTx: %v", err)
	}
	// Visible inside the transaction...
	var inTx int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM fleet_event_outbox`).Scan(&inTx); err != nil {
		t.Fatalf("count in tx: %v", err)
	}
	if inTx != 1 {
		t.Fatalf("rows visible in tx = %d, want 1", inTx)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// ...and gone once the domain write is abandoned.
	var after int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM fleet_event_outbox`).Scan(&after); err != nil {
		t.Fatalf("count after rollback: %v", err)
	}
	if after != 0 {
		t.Fatalf("rows after rollback = %d, want 0 — the event outlived the write it describes", after)
	}
}

func TestEnqueueTx_survivesCommit(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()
	ctx := context.Background()
	s := NewStore(pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.EnqueueTx(ctx, tx, "fleet.maintenance.completed", "WO-2", map[string]string{"maintenanceId": "WO-2"}); err != nil {
		t.Fatalf("EnqueueTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var eventType, eventKey string
	if err := pool.QueryRow(ctx,
		`SELECT event_type, event_key FROM fleet_event_outbox`,
	).Scan(&eventType, &eventKey); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if eventType != "fleet.maintenance.completed" || eventKey != "WO-2" {
		t.Fatalf("got (%q, %q), want (fleet.maintenance.completed, WO-2)", eventType, eventKey)
	}

	// The relay must be able to pick it up.
	rows, err := s.ClaimBatch(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("claimed %d rows, want 1", len(rows))
	}
}

// The regression this all exists for: enqueueing on the pool from inside a
// transaction leaves the event behind even when the write is rolled back. A
// post-commit Enqueue has the mirror-image failure (the write lands, the event
// never does), which no test can force but which is the same missing linkage.
func TestEnqueue_pooledIsNotBoundToTheTransaction(t *testing.T) {
	pool, done := testdb.Pool(t)
	defer done()
	ctx := context.Background()
	s := NewStore(pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.Enqueue(ctx, "fleet.maintenance.completed", "WO-3", map[string]string{"maintenanceId": "WO-3"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var after int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM fleet_event_outbox`).Scan(&after); err != nil {
		t.Fatalf("count: %v", err)
	}
	if after != 1 {
		t.Fatalf("rows after rollback = %d, want 1 — pooled Enqueue is independent of the tx by design", after)
	}
}
