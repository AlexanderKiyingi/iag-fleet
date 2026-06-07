// Command telemetry-aggregate rolls raw pings into telemetry_daily rows
// (per-vehicle, per-day distance / moving-minutes / max-speed / first-last
// timestamps). Idempotent — re-running the same day overwrites the row.
//
// Usage:
//
//	DATABASE_URL=... go run ./cmd/telemetry-aggregate
//	EVENT_BUS_ENABLED=true KAFKA_BROKERS=127.0.0.1:19092 go run ./cmd/telemetry-aggregate
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/iag/fleet-tool/backend/internal/db"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/jobs"
	fleetstore "github.com/iag/fleet-tool/backend/internal/store"
)

func main() {
	var (
		fromFlag    = flag.String("from", "", "first day to aggregate (YYYY-MM-DD, UTC). Default: yesterday")
		toFlag      = flag.String("to", "", "last day to aggregate, inclusive (YYYY-MM-DD, UTC). Default: same as --from")
		vehicleFlag = flag.String("vehicle", "", "restrict to this vehicle id (default: all vehicles with pings)")
	)
	flag.Parse()

	from, to, err := jobs.ResolveAggregateRange(*fromFlag, *toFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	operationalPool, err := db.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("connect operational Postgres: %v", err)
	}
	defer operationalPool.Close()

	var telemetryPool *pgxpool.Pool
	telemetryURL := strings.TrimSpace(os.Getenv("TELEMETRY_DATABASE_URL"))
	if telemetryURL != "" && telemetryURL != strings.TrimSpace(os.Getenv("DATABASE_URL")) {
		telemetryPool, err = db.Connect(ctx, telemetryURL)
		if err != nil {
			log.Fatalf("connect telemetry Postgres: %v", err)
		}
		defer telemetryPool.Close()
	}

	store := iot.NewSplitStore(operationalPool, telemetryPool)
	eventBus := events.NewFromEnv()
	defer func() { _ = eventBus.Close() }()

	written, eventsWritten, failed, err := jobs.AggregateTelemetry(ctx, store, eventBus, fleetstore.FuelDB{Operational: operationalPool, Telemetry: telemetryPool}, from, to, *vehicleFlag)
	if err != nil {
		log.Fatalf("aggregate: %v", err)
	}

	if written == 0 && failed == 0 {
		log.Print("no pings in range; nothing to do")
		return
	}

	log.Printf("done: %d daily rows written, %d fuel events persisted, %d failed", written, eventsWritten, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
