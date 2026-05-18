package cache

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis implements Cache using a Redis string value per key.
type Redis struct {
	c *redis.Client
}

// NewRedis connects from a URL (e.g. redis://localhost:6379/0 or rediss:// for TLS).
func NewRedis(redisURL string) (*Redis, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	// Conservative defaults for an API sidecar — tune in production if needed.
	opt.DialTimeout = 2 * time.Second
	opt.ReadTimeout = 2 * time.Second
	opt.WriteTimeout = 2 * time.Second
	c := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, err
	}
	slog.Info("redis cache connected", "addr", opt.Addr)
	return &Redis{c: c}, nil
}

// Get implements Cache.
func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := r.c.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		slog.Debug("redis get", "key", key, "err", err)
		return nil, false, nil // degrade: miss on fault
	}
	return val, true, nil
}

// Set implements Cache.
func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if err := r.c.Set(ctx, key, value, ttl).Err(); err != nil {
		slog.Debug("redis set", "key", key, "err", err)
		return err
	}
	return nil
}

// Delete implements Cache.
func (r *Redis) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return r.c.Del(ctx, keys...).Err()
}

// Close implements Cache.
func (r *Redis) Close() error {
	return r.c.Close()
}
