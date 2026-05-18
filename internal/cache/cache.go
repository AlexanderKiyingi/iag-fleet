// Package cache provides optional Redis-backed response caching for expensive
// read aggregators. When REDIS_URL is unset, [NoOp] is used — handlers behave
// as before with zero extra infrastructure.
package cache

import (
	"context"
	"time"
)

// Cache stores opaque JSON payloads with TTL-based expiry.
type Cache interface {
	// Get returns cached bytes. ok is false on miss or when Redis is unavailable.
	Get(ctx context.Context, key string) (data []byte, ok bool, err error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Delete removes keys (best-effort; ignores nil / NoOp).
	Delete(ctx context.Context, keys ...string) error
	Close() error
}

// NoOp is a no-storage implementation used when Redis is not configured.
type NoOp struct{}

func (NoOp) Get(context.Context, string) ([]byte, bool, error) { return nil, false, nil }
func (NoOp) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (NoOp) Delete(context.Context, ...string) error                  { return nil }
func (NoOp) Close() error                                             { return nil }
