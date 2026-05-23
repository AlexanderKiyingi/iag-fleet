// Command seed brings a Postgres database up to the current schema and
// optionally loads the demo seed data. It runs all forward migrations
// in db/migrations/ via internal/migrate, recording each in
// schema_migrations so subsequent invocations only apply what's new.
//
// SQL assets are embedded into the binary by the db package — deployments
// don't need to ship the .sql files separately.
//
// Usage:
//   DATABASE_URL=postgres://user:pass@host:5432/db go run ./cmd/seed
//   go run ./cmd/seed --reset            # drop tables first, then migrate + seed
//   go run ./cmd/seed --schema-only      # apply migrations, skip seed inserts
//   go run ./cmd/seed --seed-only        # skip migrations (assume tables exist)
//   go run ./cmd/seed --clear-data       # migrate + wipe seeded business data
//                                        # (preserves users, RBAC, sessions)
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/iag/fleet-tool/backend/db"
	pgdb "github.com/iag/fleet-tool/backend/internal/db"
	"github.com/iag/fleet-tool/backend/internal/migrate"

	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	databaseURL string
	reset       bool
	schemaOnly  bool
	seedOnly    bool
	clearData   bool
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := parseFlags()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgdb.Connect(ctx, cfg.databaseURL)
	if err != nil {
		slog.Error("connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if cfg.reset {
		body, err := db.Reset()
		if err != nil {
			slog.Error("read reset.sql", "err", err)
			os.Exit(1)
		}
		if err := exec(ctx, pool, body); err != nil {
			slog.Error("reset failed", "err", err)
			os.Exit(1)
		}
		slog.Info("reset.sql ok")
	}

	if !cfg.seedOnly {
		applied, err := migrate.Up(ctx, pool, db.Migrations())
		if err != nil {
			slog.Error("migrate failed", "err", err)
			os.Exit(1)
		}
		if len(applied) == 0 {
			slog.Info("schema already up to date")
		} else {
			slog.Info("migrations applied", "count", len(applied), "versions", applied)
		}
	}

	switch {
	case cfg.clearData:
		// Wipe seeded business / ops / telemetry data; keeps schema_migrations.
		body, err := db.ClearSeed()
		if err != nil {
			slog.Error("read clear_seed.sql", "err", err)
			os.Exit(1)
		}
		if err := exec(ctx, pool, body); err != nil {
			slog.Error("clear_seed failed", "err", err)
			os.Exit(1)
		}
		slog.Info("clear_seed.sql ok")
	case !cfg.schemaOnly:
		body, err := db.Seed()
		if err != nil {
			slog.Error("read seed.sql", "err", err)
			os.Exit(1)
		}
		if err := exec(ctx, pool, body); err != nil {
			slog.Error("seed failed", "err", err)
			os.Exit(1)
		}
		slog.Info("seed.sql ok")
	}

	if err := summarize(ctx, pool); err != nil {
		slog.Warn("summary failed", "err", err)
	}
}

func parseFlags() config {
	var cfg config
	flag.BoolVar(&cfg.reset, "reset", false, "drop all tables before migrating")
	flag.BoolVar(&cfg.schemaOnly, "schema-only", false, "apply migrations and skip seed.sql")
	flag.BoolVar(&cfg.seedOnly, "seed-only", false, "skip migrations; only run seed.sql (tables must exist)")
	flag.BoolVar(&cfg.clearData, "clear-data", false, "migrate + run clear_seed.sql (wipe seeded business data)")
	flag.Parse()

	cfg.databaseURL = os.Getenv("DATABASE_URL")
	if cfg.databaseURL == "" {
		slog.Error("DATABASE_URL is required (e.g. postgres://user:pass@host:5432/dbname)")
		os.Exit(2)
	}
	if cfg.schemaOnly && cfg.seedOnly {
		slog.Error("--schema-only and --seed-only are mutually exclusive")
		os.Exit(2)
	}
	if cfg.clearData && (cfg.reset || cfg.schemaOnly || cfg.seedOnly) {
		slog.Error("--clear-data is mutually exclusive with --reset, --schema-only, --seed-only")
		os.Exit(2)
	}
	return cfg
}

func exec(ctx context.Context, pool *pgxpool.Pool, body []byte) error {
	if len(body) == 0 {
		return errors.New("empty SQL body")
	}
	_, err := pool.Exec(ctx, string(body))
	return err
}

func summarize(ctx context.Context, pool *pgxpool.Pool) error {
	tables := []string{
		"drivers", "vehicles", "jmps", "cargo", "cargo_docs", "fuel_records",
		"maintenance_items", "parts", "tyres", "trips", "safety_events",
		"compliance_items", "service_requests", "task_items",
		"deployment_days", "operator_ticker", "audit_entries",
		"iot_devices", "telemetry_timeseries", "telemetry_daily", "fuel_events",
	}
	for _, t := range tables {
		var n int
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n); err != nil {
			slog.Warn("count failed", "table", t, "err", err)
			continue
		}
		slog.Info("table", "name", t, "rows", n)
	}
	return nil
}
