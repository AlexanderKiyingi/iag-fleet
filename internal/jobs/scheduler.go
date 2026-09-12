package jobs

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schedulerConfig carries the tunables the one-shot flags already expose, so
// --schedule reuses the same defaults.
type SchedulerConfig struct {
	PurgeDays    int
	LinkDays     int
	PmWithinDays int
	PmWithinKm   float64
}

// runSchedulerMode runs the maintenance jobs on fixed cadences until SIGTERM/
// SIGINT. Deploy it as a single long-lived worker (one replica) alongside the
// API — it is the periodic janitor that turns the live ping stream into rolled-
// up history, retires stale rows, and ages out statuses.
// RunSchedulerMode is the long-lived worker: it installs its own SIGTERM
// handler and blocks. cmd/fleet-jobs --schedule is the only caller; anything
// embedding the scheduler in another process wants RunScheduler, which takes a
// context that process already owns.
func RunSchedulerMode(operationalPool, telemetryPool *pgxpool.Pool, iotStore *iot.Store, eventBus *events.Bus, cfg SchedulerConfig) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d := SchedulerDeps{
		IotStore:     iotStore,
		EventBus:     eventBus,
		FuelDB:       store.FuelDB{Operational: operationalPool, Telemetry: telemetryPool},
		Repo:         store.NewRepository(operationalPool),
		PurgeDays:    cfg.PurgeDays,
		LinkDays:     cfg.LinkDays,
		PmWithinDays: cfg.PmWithinDays,
		PmWithinKm:   cfg.PmWithinKm,
	}
	RunScheduler(ctx, d)
}

type SchedulerDeps struct {
	IotStore *iot.Store
	EventBus *events.Bus
	FuelDB   store.FuelDB
	Repo     *store.Repository

	PurgeDays    int
	LinkDays     int
	PmWithinDays int
	PmWithinKm   float64
}

// scheduledTask is one job plus its cadence. initialDelay staggers the first run
// so a fresh deploy doesn't fire every heavy job at once on boot.
type scheduledTask struct {
	name         string
	interval     time.Duration
	initialDelay time.Duration
	run          func(context.Context)
}

// runTasks runs each task once (after its initialDelay), then every interval,
// until ctx is cancelled. Tasks run in their own goroutines so a slow job can't
// delay the others; each run is independently timed out by the task itself.
func runTasks(ctx context.Context, tasks []scheduledTask) {
	var wg sync.WaitGroup
	for _, t := range tasks {
		wg.Add(1)
		go func(t scheduledTask) {
			defer wg.Done()
			if t.initialDelay > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(t.initialDelay):
				}
			}
			t.run(ctx)
			tk := time.NewTicker(t.interval)
			defer tk.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tk.C:
					t.run(ctx)
				}
			}
		}(t)
	}
	wg.Wait()
}

// Defaults for the scheduler's tunables.
//
// These live here rather than only in cmd/fleet-jobs's flag definitions
// because a zero value is not a harmless "unset" for any of them. The flag
// parser refuses --purge-days below 1; a struct literal has no such guard, and
// PurgeTelemetryPings clamps 0 up to 1 — so a caller that simply omitted the
// field would retain ONE DAY of telemetry and delete the rest on the first
// nightly run. Any embedder that under-specifies now gets the same retention
// the worker has always had.
const (
	DefaultPurgeDays = 365
	DefaultLinkDays  = 90
)

// withDefaults fills in anything the caller left at zero.
func (d SchedulerDeps) withDefaults() SchedulerDeps {
	if d.PurgeDays < 1 {
		d.PurgeDays = DefaultPurgeDays
	}
	if d.LinkDays < 1 {
		d.LinkDays = DefaultLinkDays
	}
	if d.PmWithinDays < 1 {
		d.PmWithinDays = DefaultPMWithinDays
	}
	if d.PmWithinKm <= 0 {
		d.PmWithinKm = DefaultPMWithinKm
	}
	return d
}

// RunScheduler runs the maintenance jobs on their cadences until ctx is done.
func RunScheduler(ctx context.Context, d SchedulerDeps) {
	d = d.withDefaults()
	stale := envDuration("FLEET_SCHED_STALE_INTERVAL", time.Hour)
	daily := envDuration("FLEET_SCHED_DAILY_INTERVAL", 24*time.Hour)

	tasks := []scheduledTask{
		// Frequent: flip vehicles offline soon after they stop reporting (so the
		// map doesn't show them "moving" for a whole day).
		{name: "mark-stale", interval: stale, run: d.runMarkStale},
		// Nightly: aggregate yesterday → daily rollups + fuel events, link the
		// manual ledger, detect trips, then purge old raw pings.
		{name: "telemetry-daily", interval: daily, initialDelay: 2 * time.Minute, run: d.runTelemetryDaily},
		// Nightly: fleet-ops hygiene independent of telemetry.
		{name: "fleet-maintenance", interval: daily, initialDelay: 5 * time.Minute, run: d.runFleetMaintenance},
	}
	log.Printf("fleet-jobs scheduler: started (mark-stale every %s, daily jobs every %s)", stale, daily)
	runTasks(ctx, tasks)
	log.Print("fleet-jobs scheduler: stopped")
}

func (d SchedulerDeps) runMarkStale(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	n, err := MarkStaleVehiclesOffline(ctx, d.IotStore)
	if err != nil {
		log.Printf("fleet-jobs scheduler: mark-stale: %v", err)
		return
	}
	if n > 0 {
		log.Printf("fleet-jobs scheduler: mark-stale set %d vehicles offline", n)
	}
}

func (d SchedulerDeps) runTelemetryDaily(ctx context.Context) {
	from, to, err := ResolveAggregateRange("", "")
	if err != nil {
		log.Printf("fleet-jobs scheduler: aggregate range: %v", err)
		return
	}

	aggCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	if written, ev, failed, err := AggregateTelemetry(aggCtx, d.IotStore, d.EventBus, d.FuelDB, from, to, ""); err != nil {
		log.Printf("fleet-jobs scheduler: aggregate: %v", err)
	} else {
		log.Printf("fleet-jobs scheduler: aggregate %s: %d daily rows, %d fuel events, %d failed",
			from.Format(DayLayout), written, ev, failed)
	}

	if res, err := ReconcileFuel(aggCtx, d.FuelDB, d.LinkDays, ""); err != nil {
		log.Printf("fleet-jobs scheduler: reconcile-fuel: %v", err)
	} else {
		log.Print(ReconcileFuelSummary(res, nil))
	}

	if n, err := DetectTripsFromTelemetry(aggCtx, d.IotStore, d.Repo, from, to); err != nil {
		log.Printf("fleet-jobs scheduler: detect-trips: %v", err)
	} else if n > 0 {
		log.Printf("fleet-jobs scheduler: detect-trips created %d trips", n)
	}

	purgeCtx, cancelPurge := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelPurge()
	if n, err := PurgeTelemetryPings(purgeCtx, d.IotStore, d.PurgeDays); err != nil {
		log.Printf("fleet-jobs scheduler: purge: %v", err)
	} else if n > 0 {
		log.Printf("fleet-jobs scheduler: purge deleted %d pings (retain %d days)", n, d.PurgeDays)
	}
}

func (d SchedulerDeps) runFleetMaintenance(ctx context.Context) {
	withTimeout := func(fn func(context.Context) (int, error), label string) {
		c, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if n, err := fn(c); err != nil {
			log.Printf("fleet-jobs scheduler: %s: %v", label, err)
		} else if n > 0 {
			log.Printf("fleet-jobs scheduler: %s updated %d", label, n)
		}
	}

	withTimeout(func(c context.Context) (int, error) { return RecomputeComplianceStatuses(c, d.Repo) }, "recompute-compliance")
	withTimeout(func(c context.Context) (int, error) { return MarkOverdueMaintenance(c, d.Repo) }, "mark-mx-overdue")

	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if res, err := EvaluatePM(c, d.Repo, d.PmWithinDays, d.PmWithinKm); err != nil {
		log.Printf("fleet-jobs scheduler: evaluate-pm: %v", err)
	} else if res.Created > 0 {
		log.Printf("fleet-jobs scheduler: evaluate-pm created %d work orders (checked %d, skipped %d)",
			res.Created, res.Checked, res.Skipped)
	}
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}
