package migrate

import (
	"context"
	"os"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Status is what a deploy runs before it changes anything, so its failure modes
// matter more than its happy path. These use a tiny in-memory migration set
// rather than the real db/migrations, so the assertions stay readable and do
// not shift every time a migration is added.
//
//	TEST_DATABASE_URL=postgres://... go test ./internal/migrate/... -run Integration
func statusTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping migrate status integration tests")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	// A private schema per run keeps this away from the real ledger, which the
	// other integration tests depend on.
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS iag_fleet`); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS iag_fleet.schema_migrations`); err != nil {
		t.Fatalf("reset ledger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS migrate_status_probe`)
	})
	return pool
}

func twoMigrations() fstest.MapFS {
	return fstest.MapFS{
		"0001_probe.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS migrate_status_probe (id INT PRIMARY KEY)`),
		},
		"0002_probe_col.sql": &fstest.MapFile{
			Data: []byte(`ALTER TABLE migrate_status_probe ADD COLUMN IF NOT EXISTS note TEXT`),
		},
	}
}

func TestIntegration_StatusReportsPendingOnFreshLedger(t *testing.T) {
	pool := statusTestPool(t)
	ctx := context.Background()

	applied, pending, err := Status(ctx, pool, twoMigrations())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("applied = %v, want none on a fresh ledger", applied)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %v, want both migrations", pending)
	}

	// Status must not have changed the schema — it is what -dry-run reports.
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'migrate_status_probe')`,
	).Scan(&exists); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if exists {
		t.Fatal("Status applied a migration; it must only report")
	}
}

func TestIntegration_StatusAfterUpReportsNothingPending(t *testing.T) {
	pool := statusTestPool(t)
	ctx := context.Background()
	fsys := twoMigrations()

	done, err := Up(ctx, pool, fsys)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(done) != 2 {
		t.Fatalf("applied %v, want 2", done)
	}

	applied, pending, err := Status(ctx, pool, fsys)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(applied) != 2 || len(pending) != 0 {
		t.Fatalf("applied=%v pending=%v, want 2 and 0", applied, pending)
	}

	// Re-running must be a safe no-op — the deploy step is expected to be
	// runnable twice without thinking about it.
	again, err := Up(ctx, pool, fsys)
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second run applied %v, want nothing", again)
	}
}

// Editing an already-applied migration is the mistake that silently drifts a
// schema. Status must catch it during the dry run, before a deploy starts.
func TestIntegration_StatusRejectsEditedMigration(t *testing.T) {
	pool := statusTestPool(t)
	ctx := context.Background()

	if _, err := Up(ctx, pool, twoMigrations()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	edited := twoMigrations()
	edited["0001_probe.sql"] = &fstest.MapFile{
		Data: []byte(`CREATE TABLE IF NOT EXISTS migrate_status_probe (id INT PRIMARY KEY, extra TEXT)`),
	}

	_, _, err := Status(ctx, pool, edited)
	if err == nil {
		t.Fatal("Status must reject a migration whose contents changed after being applied")
	}
	if !contains(err.Error(), "contents have changed") {
		t.Fatalf("error %q should explain that the file was edited", err)
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
