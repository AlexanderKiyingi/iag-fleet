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
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	fleetdb "github.com/iag/fleet-tool/backend/db"
	"github.com/iag/fleet-tool/backend/internal/migrate"
)

// Pool connects using TEST_DATABASE_URL or skips the test.
//
// The returned pool holds an exclusive advisory lock on the test database for
// the lifetime of the test, released by the cleanup func.
//
// That is not belt-and-braces: `go test ./...` runs packages in PARALLEL, and
// every package using this helper truncates the same shared tables. Without the
// lock, one package's TruncateRegistry deletes rows another package's test just
// committed, and the suite fails intermittently in whichever test happened to be
// mid-flight. Serializing here is correct rather than merely convenient —
// these tests share one database and cannot be concurrent by construction.
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

	// Held on its own connection so the pool stays free for the test itself.
	// The wait is unbounded by design: blocking behind another package is the
	// intended behaviour, and a timeout here would just reintroduce the race.
	lockConn, err := pool.Acquire(context.Background())
	if err != nil {
		pool.Close()
		t.Fatalf("acquire exclusive test-database lock: %v", err)
	}
	if _, err := lockConn.Exec(context.Background(),
		"SELECT pg_advisory_lock($1)", exclusiveTestLockKey); err != nil {
		lockConn.Release()
		pool.Close()
		t.Fatalf("lock test database: %v", err)
	}
	release := func() {
		unlockCtx, cancelUnlock := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = lockConn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", exclusiveTestLockKey)
		cancelUnlock()
		lockConn.Release()
		pool.Close()
	}

	EnsureMigrated(t, pool)
	TruncateRegistry(t, pool)
	return pool, release
}

// exclusiveTestLockKey serializes whole integration tests across packages;
// migrationLockKey only serializes the migration step. Distinct keys so a test
// holding the database does not deadlock against its own EnsureMigrated call.
const exclusiveTestLockKey int64 = 0x1A6F1E57

// migrationLockKey namespaces the advisory lock EnsureMigrated serializes on.
// Arbitrary but must be stable across packages.
const migrationLockKey int64 = 0x1A6F1EE7

// EnsureMigrated applies pending fleet migrations.
//
// `go test ./...` runs packages in parallel and every package using this helper
// migrates the same database, so the calls are serialized on a Postgres
// advisory lock. Without it the packages race the migration ledger and whoever
// loses fails with "version already applied by another process" — which is the
// concurrency guard working correctly, just against itself.
func EnsureMigrated(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection for migration lock: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		t.Fatalf("acquire migration lock: %v", err)
	}
	defer func() {
		// Fresh context: ctx may already be done if migration ran long.
		unlockCtx, cancelUnlock := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelUnlock()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationLockKey)
	}()

	if _, err := migrate.Up(ctx, pool, fleetdb.Migrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

// preservedTables survive truncation: reference data a migration seeds and
// tests expect to be present. Everything else is test-owned and gets cleared.
var preservedTables = []string{
	"schema_migrations",
	"inspection_templates", // seeded by 0009_telemetry_inspections_pm
}

// TruncateRegistry clears fleet domain tables between tests.
//
// The table list is discovered from the catalog rather than hardcoded. It used
// to name five tables explicitly — outbox, telemetry, iot_devices, vehicles,
// drivers — while the suite writes to roughly thirty. Rows in jmps, parts,
// maintenance_items, safety_events, fuel_records and deployment_days therefore
// survived between runs and collided on their primary keys the second time the
// suite ran against the same database, which is why these tests only passed on
// a freshly created one. Discovering the list keeps a new migration from
// quietly reintroducing that.
func TruncateRegistry(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// current_schemas(false) follows the search_path the app actually uses, so
	// this works whether the domain tables live in public or in iag_fleet.
	rows, err := pool.Query(ctx, `
		SELECT quote_ident(schemaname) || '.' || quote_ident(tablename)
		  FROM pg_tables
		 WHERE schemaname = ANY(current_schemas(false))
		   AND NOT (tablename = ANY($1::text[]))
		 ORDER BY 1
	`, preservedTables)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	if len(tables) == 0 {
		return
	}

	// One statement: TRUNCATE takes an exclusive lock per table, and doing them
	// together also sidesteps ordering problems between FK-linked tables.
	if _, err := pool.Exec(ctx,
		"TRUNCATE TABLE "+strings.Join(tables, ", ")+" RESTART IDENTITY CASCADE",
	); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
