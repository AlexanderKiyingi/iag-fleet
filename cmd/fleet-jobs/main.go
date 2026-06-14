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
	"strings"
	"time"

	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/config"
	"github.com/iag/fleet-tool/backend/internal/db"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/jobs"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/warehouseclient"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	doAll := flag.Bool("all", false, "run telemetry aggregate (default range) then purge")
	doAgg := flag.Bool("aggregate", false, "roll raw pings into telemetry_daily / fuel_events")
	doPurge := flag.Bool("purge", false, "delete raw pings older than retention")
	doLink := flag.Bool("link-fuel", false, "match telemetry refuel events to fuel_records")
	doStale := flag.Bool("mark-stale", false, "set moving/idle vehicles offline when telemetry is stale")
	doDetectTrips := flag.Bool("detect-trips", false, "create auto_generated trips from telemetry in range")
	doEvaluatePM := flag.Bool("evaluate-pm", false, "create work orders for due PM schedules")
	doMarkMxOverdue := flag.Bool("mark-mx-overdue", false, "mark scheduled work orders past date as overdue")
	doRecomputeCompliance := flag.Bool("recompute-compliance", false, "derive compliance status from expiry dates")
	doReconcileWh := flag.Bool("reconcile-warehouse-stock", false, "refresh parts.stock from iag-warehouse on-hand (delegation projection backstop)")
	pmWithinDays := flag.Int("pm-within-days", jobs.DefaultPMWithinDays, "PM evaluate/due lookahead days")
	pmWithinKm := flag.Float64("pm-within-km", jobs.DefaultPMWithinKm, "PM evaluate/due odometer lookahead km")
	linkDays := flag.Int("link-days", 90, "lookback days for --link-fuel")
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
	runLink := *doLink
	runStale := *doAll || *doStale
	runDetect := *doAll || *doDetectTrips
	runEvaluatePM := *doEvaluatePM
	runMarkMxOverdue := *doMarkMxOverdue
	runRecomputeCompliance := *doRecomputeCompliance
	runReconcileWh := *doReconcileWh
	if !runAgg && !runPurge && !runLink && !runStale && !runDetect &&
		!runEvaluatePM && !runMarkMxOverdue && !runRecomputeCompliance && !runReconcileWh {
		fmt.Fprintln(os.Stderr, "specify --all, --aggregate, --purge, --mark-stale, --detect-trips, --evaluate-pm, --mark-mx-overdue, --recompute-compliance, --reconcile-warehouse-stock, and/or --link-fuel")
		flag.Usage()
		os.Exit(2)
	}

	ctxAgg, cancelAgg := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancelAgg()

	operationalPool, err := db.Connect(ctxAgg, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("connect operational Postgres: %v", err)
	}
	defer operationalPool.Close()

	var telemetryPool *pgxpool.Pool
	telemetryURL := strings.TrimSpace(os.Getenv("TELEMETRY_DATABASE_URL"))
	if telemetryURL != "" && telemetryURL != strings.TrimSpace(os.Getenv("DATABASE_URL")) {
		telemetryPool, err = db.Connect(ctxAgg, telemetryURL)
		if err != nil {
			log.Fatalf("connect telemetry Postgres: %v", err)
		}
		defer telemetryPool.Close()
	}
	iotStore := iot.NewSplitStore(operationalPool, telemetryPool)

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
		written, ev, failed, err := jobs.AggregateTelemetry(ctxAgg, iotStore, eventBus, store.FuelDB{Operational: operationalPool, Telemetry: telemetryPool}, from, to, *vehicleFlag)
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
		if *doAll {
			result, err := jobs.ReconcileFuel(ctxAgg, store.FuelDB{Operational: operationalPool, Telemetry: telemetryPool}, *linkDays, "")
			if err != nil {
				log.Printf("fleet-jobs reconcile-fuel: %v", err)
			} else {
				log.Print(jobs.ReconcileFuelSummary(result, nil))
			}
		}
	}

	if runLink {
		result, err := jobs.ReconcileFuel(ctxAgg, store.FuelDB{Operational: operationalPool, Telemetry: telemetryPool}, *linkDays, "")
		if err != nil {
			log.Fatalf("link-fuel: %v", err)
		}
		log.Print(jobs.ReconcileFuelSummary(result, nil))
	}

	if runPurge {
		ctxPurge, cancelPurge := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancelPurge()
		n, err := jobs.PurgeTelemetryPings(ctxPurge, iotStore, *purgeDays)
		if err != nil {
			log.Fatalf("purge: %v", err)
		}
		log.Printf("fleet-jobs purge: deleted %d raw pings (retain %d days)", n, *purgeDays)
	}

	if runStale {
		ctxStale, cancelStale := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancelStale()
		n, err := jobs.MarkStaleVehiclesOffline(ctxStale, iotStore)
		if err != nil {
			log.Fatalf("mark-stale: %v", err)
		}
		log.Printf("fleet-jobs mark-stale: %d vehicles set offline", n)
	}

	if runDetect {
		from, to, err := jobs.ResolveAggregateRange(*fromFlag, *toFlag)
		if err != nil {
			log.Fatalf("detect-trips range: %v", err)
		}
		repo := store.NewRepository(operationalPool)
		n, err := jobs.DetectTripsFromTelemetry(ctxAgg, iotStore, repo, from, to)
		if err != nil {
			log.Fatalf("detect-trips: %v", err)
		}
		log.Printf("fleet-jobs detect-trips: created %d trips (%s .. %s)",
			n, from.Format(jobs.DayLayout), to.Add(-time.Nanosecond).Format(jobs.DayLayout))
	}

	repo := store.NewRepository(operationalPool)
	if runRecomputeCompliance {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		n, err := jobs.RecomputeComplianceStatuses(ctx, repo)
		cancel()
		if err != nil {
			log.Fatalf("recompute-compliance: %v", err)
		}
		log.Printf("fleet-jobs recompute-compliance: %d rows updated", n)
	}
	if runMarkMxOverdue {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		n, err := jobs.MarkOverdueMaintenance(ctx, repo)
		cancel()
		if err != nil {
			log.Fatalf("mark-mx-overdue: %v", err)
		}
		log.Printf("fleet-jobs mark-mx-overdue: %d work orders marked overdue", n)
	}
	if runEvaluatePM {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		res, err := jobs.EvaluatePM(ctx, repo, *pmWithinDays, *pmWithinKm)
		cancel()
		if err != nil {
			log.Fatalf("evaluate-pm: %v", err)
		}
		log.Printf("fleet-jobs evaluate-pm: checked %d, created %d WOs, skipped %d",
			res.Checked, res.Created, res.Skipped)
	}
	if runReconcileWh {
		cfg, err := config.Load()
		if err != nil {
			log.Fatalf("reconcile-warehouse-stock: config: %v", err)
		}
		if !cfg.WarehouseDelegationEnabled {
			log.Fatal("reconcile-warehouse-stock: set WAREHOUSE_DELEGATION_ENABLED=true and the warehouse credentials")
		}
		wh := warehouseclient.New(warehouseclient.Options{
			BaseURL:      cfg.WarehouseBaseURL,
			Audience:     cfg.WarehouseAudience,
			TokenURL:     cfg.AuthTokenURL,
			ClientID:     cfg.ServiceClientID,
			ClientSecret: cfg.ServiceClientSecret,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		res, err := jobs.ReconcileWarehouseStock(ctx, operationalPool, wh)
		cancel()
		if err != nil {
			log.Fatalf("reconcile-warehouse-stock: %v", err)
		}
		log.Printf("fleet-jobs reconcile-warehouse-stock: checked %d, updated %d, errors %d",
			res.Checked, res.Updated, res.Errors)
	}
}
