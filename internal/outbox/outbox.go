// Package outbox implements the transactional outbox pattern for fleet events.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Row is a pending or completed outbox entry.
type Row struct {
	ID          int64
	EventType   string
	EventKey    string
	Payload     json.RawMessage
	CreatedAt   time.Time
	AvailableAt time.Time
	Attempts    int
	LastError   string
}

// Store wraps the fleet_event_outbox table.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Enqueue(ctx context.Context, eventType, key string, payload any) error {
	if s == nil || s.pool == nil {
		return ErrNotEnqueued
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO fleet_event_outbox (event_type, event_key, payload)
		VALUES ($1, $2, $3::jsonb)
	`, eventType, nullable(key), body)
	return err
}

func (s *Store) ClaimBatch(ctx context.Context, limit int, backoff time.Duration) ([]Row, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM fleet_event_outbox
			WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL AND available_at <= NOW()
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE fleet_event_outbox o
		SET attempts = o.attempts + 1,
		    available_at = NOW() + $2::interval
		FROM due
		WHERE o.id = due.id
		RETURNING o.id, o.event_type, o.event_key, o.payload, o.created_at,
		          o.available_at, o.attempts, COALESCE(o.last_error, '')
	`, limit, fmt.Sprintf("%d milliseconds", backoff.Milliseconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		var key *string
		if err := rows.Scan(&r.ID, &r.EventType, &key, &r.Payload, &r.CreatedAt,
			&r.AvailableAt, &r.Attempts, &r.LastError); err != nil {
			return nil, err
		}
		if key != nil {
			r.EventKey = *key
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkDispatched(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE fleet_event_outbox
		SET dispatched_at = NOW(), last_error = NULL
		WHERE id = $1
	`, id)
	return err
}

func (s *Store) MarkFailed(ctx context.Context, id int64, errMsg string, retryDelay time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE fleet_event_outbox
		SET last_error = $1, available_at = NOW() + $2::interval
		WHERE id = $3
	`, errMsg, fmt.Sprintf("%d milliseconds", retryDelay.Milliseconds()), id)
	return err
}

// DeadLetter parks a row that has exhausted its retries: it stops appearing in
// ClaimBatch (dead_lettered_at IS NOT NULL) but is retained with its last error
// for inspection / manual replay.
func (s *Store) DeadLetter(ctx context.Context, id int64, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE fleet_event_outbox
		SET dead_lettered_at = NOW(), last_error = $1
		WHERE id = $2
	`, errMsg, id)
	return err
}

// Dispatcher writes a drained outbox row to Kafka.
type Dispatcher interface {
	DispatchOutbox(ctx context.Context, row Row) error
}

// claimStore is the subset of *Store the drain loop needs. Narrowed to an
// interface so drainOnce's retry/dead-letter branching is unit-testable.
type claimStore interface {
	ClaimBatch(ctx context.Context, limit int, backoff time.Duration) ([]Row, error)
	MarkDispatched(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, errMsg string, retryDelay time.Duration) error
	DeadLetter(ctx context.Context, id int64, errMsg string) error
}

// defaultMaxAttempts bounds retries before a poison row is dead-lettered. With
// exponential backoff capped at maxBackoff, this spans hours before giving up.
const defaultMaxAttempts = 12

// Publisher periodically drains the outbox.
type Publisher struct {
	store       claimStore
	dispatcher  Dispatcher
	tick        time.Duration
	batch       int
	maxBackoff  time.Duration
	maxAttempts int
}

func NewPublisher(store *Store, d Dispatcher) *Publisher {
	return &Publisher{
		store:       store,
		dispatcher:  d,
		tick:        2 * time.Second,
		batch:       32,
		maxBackoff:  5 * time.Minute,
		maxAttempts: defaultMaxAttempts,
	}
}

func (p *Publisher) Run(ctx context.Context) {
	if p == nil || p.store == nil || p.dispatcher == nil {
		return
	}
	ticker := time.NewTicker(p.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := p.drainOnce(ctx)
			if err != nil {
				slog.Warn("outbox drain", "err", err)
				continue
			}
			if n >= p.batch {
				_, _ = p.drainOnce(ctx)
			}
		}
	}
}

func (p *Publisher) drainOnce(ctx context.Context) (int, error) {
	rows, err := p.store.ClaimBatch(ctx, p.batch, time.Second)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		if err := p.dispatcher.DispatchOutbox(ctx, r); err != nil {
			// r.Attempts is post-increment (ClaimBatch bumped it). Once it
			// reaches the cap, stop retrying and dead-letter so one poison row
			// can't block the drain forever.
			if r.Attempts >= p.maxAttempts {
				_ = p.store.DeadLetter(ctx, r.ID, err.Error())
				slog.Error("outbox event dead-lettered after max attempts",
					"id", r.ID, "type", r.EventType, "attempts", r.Attempts, "err", err)
				continue
			}
			delay := backoffFor(r.Attempts, p.maxBackoff)
			_ = p.store.MarkFailed(ctx, r.ID, err.Error(), delay)
			slog.Warn("outbox dispatch failed", "id", r.ID, "type", r.EventType, "attempts", r.Attempts, "err", err)
			continue
		}
		_ = p.store.MarkDispatched(ctx, r.ID)
	}
	return len(rows), nil
}

func backoffFor(attempts int, max time.Duration) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := time.Duration(math.Pow(2, float64(attempts))) * time.Second
	if d > max {
		return max
	}
	return d
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var ErrNotEnqueued = errors.New("outbox: publisher not configured")
