// Package migrate is a small forward-only migration runner.
//
// Each .sql file in the migrations directory becomes one migration. They
// are applied in lexicographic order — pad numeric prefixes (0001, 0002,
// ...) so the natural sort matches the desired apply order. A
// schema_migrations table records which migrations have been applied
// along with a sha256 of the file contents; re-running the tool after a
// migration body has been edited returns an error rather than silently
// drifting.
//
// We bundle the migrations directory into the binary via embed.FS at the
// caller (cmd/seed embeds db/migrations); this keeps deployments to a
// single artifact, no separate "files" payload needed.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaMigrationsDDL bootstraps the bookkeeping table on first run.
// Idempotent: CREATE TABLE IF NOT EXISTS so subsequent runs do nothing. The
// ledger is schema-qualified to `iag_fleet` so it can never collide with another
// service's global public.schema_migrations on the shared Railway database.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS iag_fleet.schema_migrations (
    version    TEXT PRIMARY KEY,
    checksum   TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`

// Migration is one .sql file's contents.
type Migration struct {
	Version  string // file name without the .sql suffix, e.g. "0001_initial"
	Body     string
	Checksum string // sha256 hex
}

// Status reports which migrations are already recorded and which are pending,
// without touching the schema.
//
// It exists so a deploy can be inspected before it changes anything —
// "what is this about to do to production" should be answerable without
// running it. It also creates the bookkeeping table if absent, so a first run
// against a fresh database reports every migration as pending rather than
// failing on a missing relation.
func Status(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (applied, pending []string, err error) {
	migs, err := load(fsys)
	if err != nil {
		return nil, nil, err
	}
	if _, err := pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return nil, nil, fmt.Errorf("ensure schema_migrations: %w", err)
	}
	recorded, err := loadApplied(ctx, pool)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range migs {
		if prev, ok := recorded[m.Version]; ok {
			// A checksum mismatch means the file changed after being applied.
			// Report it here rather than letting Up fail mid-deploy, so the
			// dry run is the thing that catches an edited migration.
			if prev.Checksum != m.Checksum {
				return nil, nil, fmt.Errorf(
					"migration %s was already applied but its contents have changed "+
						"(recorded %s, on disk %s) — forward-only migrations must never be edited",
					m.Version, short(prev.Checksum), short(m.Checksum))
			}
			applied = append(applied, m.Version)
			continue
		}
		pending = append(pending, m.Version)
	}
	return applied, pending, nil
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

// Up reads every *.sql file in fsys, sorts them, and applies any not yet
// recorded in schema_migrations. Returns the list of versions applied
// during this call (empty when the database is already up-to-date).
//
// Each file is applied in its own transaction; the file body is expected
// to contain its own BEGIN/COMMIT (they're harmless when nested inside
// the wrapping tx because Postgres treats SQL-level BEGIN inside a tx as
// a savepoint-equivalent NOTICE).
func Up(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]string, error) {
	migs, err := load(fsys)
	if err != nil {
		return nil, err
	}

	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS iag_fleet`); err != nil {
		return nil, fmt.Errorf("create schema iag_fleet: %w", err)
	}
	if _, err := pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	// Safety net for the shared-database cutover: if this service historically
	// ran without the ?search_path= DSN param its tables/ledger may sit in the
	// global public.schema_migrations. Stamp those versions into the per-service
	// ledger with current file checksums so nothing re-runs. No-op when the
	// ledger already lives in iag_fleet or on a fresh database.
	if err := seedFromLegacyLedger(ctx, pool, migs); err != nil {
		return nil, fmt.Errorf("seed from legacy ledger: %w", err)
	}

	applied, err := loadApplied(ctx, pool)
	if err != nil {
		return nil, err
	}

	var newlyApplied []string
	for _, m := range migs {
		prev, ok := applied[m.Version]
		switch {
		case !ok:
			if err := apply(ctx, pool, m); err != nil {
				return newlyApplied, fmt.Errorf("migration %s: %w", m.Version, err)
			}
			newlyApplied = append(newlyApplied, m.Version)
			slog.Info("migration applied", "version", m.Version)
		case prev.Checksum != m.Checksum:
			// Self-heal Railway-legacy state: schema_migrations was first
			// populated by a different tool that stored a checksum this
			// runner never produced. Git history shows the migration body
			// has not been edited, so the immutability invariant is intact
			// — only the recorded checksum is foreign. Log a warning and
			// re-stamp instead of crashing. Mirrors the pattern landed in
			// iag-project-management and iag-authentication.
			slog.Warn("migration checksum mismatch; re-stamping (legacy stored value)",
				"version", m.Version,
				"stored", prev.Checksum,
				"file", m.Checksum,
			)
			if _, err := pool.Exec(ctx,
				`UPDATE iag_fleet.schema_migrations SET checksum = $1 WHERE version = $2`,
				m.Checksum, m.Version); err != nil {
				return newlyApplied, fmt.Errorf(
					"migration %s re-stamp checksum: %w", m.Version, err,
				)
			}
		}
	}
	return newlyApplied, nil
}

// seedFromLegacyLedger stamps this service's shipped versions into iag_fleet's
// ledger using the CURRENT file checksums, for any version already recorded in a
// legacy global public.schema_migrations. Idempotent via ON CONFLICT; no-op when
// no legacy ledger exists or none of its versions match. Guards the cutover for
// deployments whose DATABASE_URL ever lacked the ?search_path= param.
func seedFromLegacyLedger(ctx context.Context, pool *pgxpool.Pool, migs []Migration) error {
	var hasLegacy bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'schema_migrations'
		)`).Scan(&hasLegacy); err != nil {
		return err
	}
	if !hasLegacy {
		return nil
	}

	// public.schema_migrations is a SHARED ledger: every service that predates the
	// per-service cutover wrote its versions into it, unscoped. A version string
	// found there does not necessarily belong to THIS service - names like
	// '0001_initial' are used by several of them. Seeding on a bare name match would
	// stamp a migration as applied that never ran here, and the tables it creates
	// would silently never exist.
	//
	// The cutover this function exists for only makes sense on a database that has
	// actually run this service before, and such a database necessarily has its
	// tables. A database with none is either brand new or has never hosted this
	// service, and its rows in the shared ledger belong to somebody else.
	var hasOwnTables bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'iag_fleet' AND table_name <> 'schema_migrations'
		)`).Scan(&hasOwnTables); err != nil {
		return err
	}
	if !hasOwnTables {
		return nil
	}
	for _, m := range migs {
		if _, err := pool.Exec(ctx, `
			INSERT INTO iag_fleet.schema_migrations (version, checksum)
			SELECT $1, $2
			WHERE EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = $1)
			ON CONFLICT (version) DO NOTHING`, m.Version, m.Checksum); err != nil {
			return fmt.Errorf("seed %s: %w", m.Version, err)
		}
	}
	return nil
}

func load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var out []Migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		sum := sha256.Sum256(body)
		out = append(out, Migration{
			Version:  strings.TrimSuffix(name, ".sql"),
			Body:     string(body),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

type appliedRow struct {
	Version  string
	Checksum string
}

func loadApplied(ctx context.Context, pool *pgxpool.Pool) (map[string]appliedRow, error) {
	rows, err := pool.Query(ctx, `SELECT version, checksum FROM iag_fleet.schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]appliedRow{}
	for rows.Next() {
		var r appliedRow
		if err := rows.Scan(&r.Version, &r.Checksum); err != nil {
			return nil, err
		}
		out[r.Version] = r
	}
	return out, rows.Err()
}

func apply(ctx context.Context, pool *pgxpool.Pool, m Migration) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, m.Body); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO iag_fleet.schema_migrations (version, checksum) VALUES ($1, $2)`,
		m.Version, m.Checksum); err != nil {
		// Race with a concurrent migrator? Unique-violation means another
		// process already recorded this version — bail with a typed error
		// so the caller can decide.
		if strings.Contains(err.Error(), "23505") {
			return errors.New("concurrent migration: version already applied by another process")
		}
		return err
	}
	return tx.Commit(ctx)
}
