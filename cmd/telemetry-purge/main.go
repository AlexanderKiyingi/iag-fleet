// Command telemetry-purge drops telemetry_pings older than the configured
// retention window. Intended for nightly cron:
//
//	DATABASE_URL=... go run ./cmd/telemetry-purge --days 365
//	DATABASE_URL=... go run ./cmd/fleet-jobs --purge --purge-days 365
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/iag/fleet-tool/backend/internal/db"
	"github.com/iag/fleet-tool/backend/internal/iot"
	"github.com/iag/fleet-tool/backend/internal/jobs"
)

func main() {
	days := flag.Int("days", 365, "retain pings newer than this many days")
	flag.Parse()
	if *days < 1 {
		log.Fatal("--days must be >= 1")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := db.Connect(ctx, "")
	if err != nil {
		log.Fatalf("connect Postgres: %v", err)
	}
	defer pool.Close()

	store := iot.NewStore(pool)
	cutoff := time.Now().UTC().Add(-time.Duration(*days) * 24 * time.Hour)

	n, err := jobs.PurgeTelemetryPings(ctx, store, *days)
	if err != nil {
		log.Fatalf("purge: %v", err)
	}
	log.Printf("telemetry-purge: deleted %d pings older than %s", n, cutoff.Format(time.RFC3339))
}
