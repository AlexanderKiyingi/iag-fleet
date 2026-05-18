// Command fleet-jobs runs scheduled telemetry maintenance in one process —
// suitable for cron / Kubernetes CronJob / systemd timer.
//
//	DATABASE_URL=... go run ./cmd/fleet-jobs --all
//	DATABASE_URL=... EVENT_BUS_ENABLED=true KAFKA_BROKERS=127.0.0.1:19092 go run ./cmd/fleet-jobs --aggregate
//
// Order for --all: aggregate (yesterday UTC) first, then purge.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/iag/fleet-tool/backend/internal/db"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/iot"
	"github.com/iag/fleet-tool/backend/internal/jobs"
)

func main() {
	doAll := flag.Bool("all", false, "run telemetry aggregate (default range) then purge")
	doAgg := flag.Bool("aggregate", false, "roll raw pings into telemetry_daily / fuel_events")
	doPurge := flag.Bool("purge", false, "delete raw pings older than retention")
	purgeDays := flag.Int("purge-days", 365, "retention window for --purge / --all (days)")
	fromFlag := flag.String("from", "", "aggregate: first day YYYY-MM-DD UTC (default yesterday)")
	toFlag := flag.String("to", "", "aggregate: last day inclusive YYYY-MM-DD UTC")
	vehicleFlag := flag.String("vehicle", "", "aggregate: restrict to one vehicle id")
	flag.Parse()

	if *purgeDays < 1 {
		log.Fatal("--purge-days must be >= 1")
	}

	runAgg := *doAll || *doAgg
	runPurge := *doAll || *doPurge
	if !runAgg && !runPurge {
		fmt.Fprintln(os.Stderr, "specify --all, --aggregate, and/or --purge")
		flag.Usage()
		os.Exit(2)
	}

	ctxAgg, cancelAgg := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancelAgg()

	pool, err := db.Connect(ctxAgg, "")
	if err != nil {
		log.Fatalf("connect Postgres: %v", err)
	}
	defer pool.Close()
	store := iot.NewStore(pool)

	eventBus := events.NewFromEnv()
	defer func() { _ = eventBus.Close() }()
	if eventBus.Enabled() {
		log.Print("fleet-jobs: Kafka event bus enabled")
	}

	if runAgg {
		from, to, err := jobs.ResolveAggregateRange(*fromFlag, *toFlag)
		if err != nil {
			log.Fatalf("aggregate range: %v", err)
		}
		log.Printf("fleet-jobs: aggregating %s .. %s", from.Format(jobs.DayLayout), to.Add(-time.Nanosecond).Format(jobs.DayLayout))
		written, ev, failed, err := jobs.AggregateTelemetry(ctxAgg, store, eventBus, from, to, *vehicleFlag)
		if err != nil {
			log.Fatalf("aggregate: %v", err)
		}
		if written == 0 && failed == 0 {
			log.Print("fleet-jobs aggregate: nothing to do")
		} else {
			log.Printf("fleet-jobs aggregate: %d daily rows, %d fuel events inserted, %d failed", written, ev, failed)
			if failed > 0 {
				os.Exit(1)
			}
		}
	}

	if runPurge {
		ctxPurge, cancelPurge := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancelPurge()
		n, err := jobs.PurgeTelemetryPings(ctxPurge, store, *purgeDays)
		if err != nil {
			log.Fatalf("purge: %v", err)
		}
		log.Printf("fleet-jobs purge: deleted %d raw pings (retain %d days)", n, *purgeDays)
	}
}
