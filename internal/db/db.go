// Package db owns the Postgres connection pool. The pool is created once at
// startup from $DATABASE_URL and shared across handlers via gin.Context.
package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect parses the URL and pings the database to fail fast on bad config.
// Pass an empty url to read from $DATABASE_URL.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		return nil, errors.New("DATABASE_URL is empty")
	}
	if err := validateDatabaseURL(url); err != nil {
		return nil, err
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = intEnv("DB_MAX_CONNS", 50)
	cfg.MinConns = intEnv("DB_MIN_CONNS", 5)
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// validateDatabaseURL rejects local dev DSNs when running in production or on Railway.
func validateDatabaseURL(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil
	}
	onRailway := os.Getenv("RAILWAY_ENVIRONMENT") != "" || os.Getenv("RAILWAY_PROJECT_ID") != ""
	inRelease := strings.EqualFold(os.Getenv("GIN_MODE"), "release")
	if !onRailway && !inRelease {
		return nil
	}
	return fmt.Errorf(
		"DATABASE_URL points at %s — use your hosted Postgres URL, not localhost. "+
			"On Railway: add a Postgres plugin, delete any manual DATABASE_URL copied from .env.example, "+
			"then set DATABASE_URL to the plugin variable reference (e.g. ${{Postgres.DATABASE_URL}}). "+
			"See docs/RAILWAY.md",
		host,
	)
}

func intEnv(key string, fallback int32) int32 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return int32(n)
}
