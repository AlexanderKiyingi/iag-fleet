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
// Idempotent: CREATE TABLE IF NOT EXISTS so subsequent runs do nothing.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
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

// Up reads every *.sql file in fsys, sorts them, and applies any not yet
// recorded in schema_migrations. Returns the list of versions applied
// during this call (empty when the database is already up-to-date).
//
// Each file is applied in its own transaction; the file body is expected
// to contain its own BEGIN/COMMIT (they're harmless when nested inside
// the wrapping tx because Postgres treats SQL-level BEGIN inside a tx as
// a savepoint-equivalent NOTICE).
func Up(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]string, error) {
	if _, err := pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	migs, err := load(fsys)
	if err != nil {
		return nil, err
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
			return newlyApplied, fmt.Errorf(
				"migration %s checksum mismatch: stored=%s file=%s — migrations are immutable; create a new file instead of editing %s",
				m.Version, prev.Checksum, m.Checksum, m.Version,
			)
		}
	}
	return newlyApplied, nil
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
	rows, err := pool.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
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
		`INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`,
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
