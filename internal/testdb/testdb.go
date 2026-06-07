// Package testdb helpers integration tests against a real Postgres instance.
//
// Run with:
//
//	TEST_DATABASE_URL=postgres://svc_iag_fleet:iag_fleet_dev@localhost:5432/iag_platform?sslmode=disable \
//	  go test ./internal/handlers/... -run Integration -v
package testdb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	fleetdb "github.com/iag/fleet-tool/backend/db"
	"github.com/iag/fleet-tool/backend/internal/migrate"
)

// Pool connects using TEST_DATABASE_URL or skips the test.
func Pool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	EnsureMigrated(t, pool)
	TruncateRegistry(t, pool)
	return pool, func() { pool.Close() }
}

// EnsureMigrated applies pending fleet migrations.
func EnsureMigrated(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := migrate.Up(ctx, pool, fleetdb.Migrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

// TruncateRegistry clears vehicle registry tables between tests.
func TruncateRegistry(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tables := []string{
		"fleet_event_outbox",
		"telemetry_timeseries",
		"iot_devices",
		"vehicles",
		"drivers",
	}
	for _, tbl := range tables {
		if _, err := pool.Exec(ctx, "TRUNCATE TABLE "+tbl+" RESTART IDENTITY CASCADE"); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}
