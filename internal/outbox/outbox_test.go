package outbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeStore stands in for *Store so drainOnce's retry/dead-letter branching can
// be tested without Postgres. ClaimBatch returns a fixed batch once, then empty.
type fakeStore struct {
	batch      []Row
	served     bool
	dispatched []int64
	failed     []int64
	deadLetter []int64
}

func (f *fakeStore) ClaimBatch(_ context.Context, _ int, _ time.Duration) ([]Row, error) {
	if f.served {
		return nil, nil
	}
	f.served = true
	return f.batch, nil
}
func (f *fakeStore) MarkDispatched(_ context.Context, id int64) error {
	f.dispatched = append(f.dispatched, id)
	return nil
}
func (f *fakeStore) MarkFailed(_ context.Context, id int64, _ string, _ time.Duration) error {
	f.failed = append(f.failed, id)
	return nil
}
func (f *fakeStore) DeadLetter(_ context.Context, id int64, _ string) error {
	f.deadLetter = append(f.deadLetter, id)
	return nil
}

// dispatcher fails for ids in failIDs, succeeds otherwise.
type fakeDispatcher struct{ failIDs map[int64]bool }

func (d fakeDispatcher) DispatchOutbox(_ context.Context, row Row) error {
	if d.failIDs[row.ID] {
		return errors.New("boom")
	}
	return nil
}

func newPublisher(store claimStore, d Dispatcher) *Publisher {
	return &Publisher{store: store, dispatcher: d, batch: 32, maxBackoff: time.Minute, maxAttempts: defaultMaxAttempts}
}

func TestDrainMarksDispatchedOnSuccess(t *testing.T) {
	s := &fakeStore{batch: []Row{{ID: 1, Attempts: 1}}}
	p := newPublisher(s, fakeDispatcher{failIDs: nil})

	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(s.dispatched) != 1 || s.dispatched[0] != 1 {
		t.Fatalf("expected id 1 dispatched, got %v", s.dispatched)
	}
	if len(s.failed) != 0 || len(s.deadLetter) != 0 {
		t.Fatalf("success path must not fail/dead-letter (failed=%v dlq=%v)", s.failed, s.deadLetter)
	}
}

func TestDrainRetriesBelowMaxAttempts(t *testing.T) {
	// Attempts under the cap → MarkFailed (backoff retry), not dead-letter.
	s := &fakeStore{batch: []Row{{ID: 7, Attempts: defaultMaxAttempts - 1}}}
	p := newPublisher(s, fakeDispatcher{failIDs: map[int64]bool{7: true}})

	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(s.failed) != 1 || s.failed[0] != 7 {
		t.Fatalf("expected id 7 retried via MarkFailed, got failed=%v", s.failed)
	}
	if len(s.deadLetter) != 0 {
		t.Fatalf("must not dead-letter below max attempts, got %v", s.deadLetter)
	}
}

func TestDrainDeadLettersAtMaxAttempts(t *testing.T) {
	// Attempts at the cap and still failing → dead-letter, stop retrying.
	s := &fakeStore{batch: []Row{{ID: 9, Attempts: defaultMaxAttempts}}}
	p := newPublisher(s, fakeDispatcher{failIDs: map[int64]bool{9: true}})

	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(s.deadLetter) != 1 || s.deadLetter[0] != 9 {
		t.Fatalf("expected id 9 dead-lettered, got %v", s.deadLetter)
	}
	if len(s.failed) != 0 {
		t.Fatalf("dead-lettered row must not also be MarkFailed, got %v", s.failed)
	}
}

// The adaptive poll interval trades a little latency on the first event after a
// quiet spell for a large cut in idle write traffic against the shared
// Postgres. These pin both halves of that trade.
func TestNextOutboxInterval(t *testing.T) {
	base := 2 * time.Second

	// Doubles while idle...
	if got := nextOutboxInterval(base, base); got != 4*time.Second {
		t.Errorf("first idle step = %v, want 4s", got)
	}
	if got := nextOutboxInterval(4*time.Second, base); got != 8*time.Second {
		t.Errorf("second idle step = %v, want 8s", got)
	}

	// ...but never past the cap, however long the outbox stays quiet. An
	// unbounded backoff would eventually make the first event of the morning
	// arrive minutes late.
	interval := base
	for i := 0; i < 50; i++ {
		interval = nextOutboxInterval(interval, base)
	}
	if interval != outboxIdleBackoffMax {
		t.Errorf("settled at %v, want the %v cap", interval, outboxIdleBackoffMax)
	}

	// A caller that somehow passes an interval below base must not be dragged
	// below the configured tick.
	if got := nextOutboxInterval(time.Millisecond, base); got < base {
		t.Errorf("interval %v fell below the base tick %v", got, base)
	}
}

// The cap has to stay short: it is added latency on a user-visible path when a
// service has been quiet. If someone raises it, this should make them think.
func TestOutboxIdleBackoffStaysShort(t *testing.T) {
	if outboxIdleBackoffMax > 10*time.Second {
		t.Errorf("idle backoff cap is %v — that is how late the first event "+
			"after a quiet period can be; keep it under 10s or move to LISTEN/NOTIFY",
			outboxIdleBackoffMax)
	}
}
