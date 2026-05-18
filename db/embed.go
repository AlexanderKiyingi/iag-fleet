// Package db owns embedded SQL assets for migrations and seed/reset.
// cmd/seed pulls the migration directory and the seed/reset bodies from
// here so the binary ships as a single self-contained artifact.
package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

//go:embed seed.sql reset.sql clear_seed.sql
var staticFS embed.FS

// Migrations returns the migrations directory rooted at "."
// (i.e. the .sql files appear at the FS root).
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic("db: migrations sub: " + err.Error())
	}
	return sub
}

// Seed returns the demo-seed body.
func Seed() ([]byte, error) {
	return staticFS.ReadFile("seed.sql")
}

// Reset returns the destructive teardown body used by `cmd/seed --reset`.
func Reset() ([]byte, error) {
	return staticFS.ReadFile("reset.sql")
}

// ClearSeed returns the body of clear_seed.sql — used by
// `cmd/seed --clear-data` to wipe every business / ops / telemetry table
// while preserving users, RBAC, sessions, tokens, and schema_migrations.
func ClearSeed() ([]byte, error) {
	return staticFS.ReadFile("clear_seed.sql")
}
