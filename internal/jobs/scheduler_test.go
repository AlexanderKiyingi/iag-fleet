package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunTasksInitialKickThenStops(t *testing.T) {
	var n atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	// Long interval so only the initial run fires before we cancel.
	tasks := []scheduledTask{{
		name:     "t",
		interval: time.Hour,
		run:      func(context.Context) { n.Add(1) },
	}}

	go func() { time.Sleep(40 * time.Millisecond); cancel() }()
	runTasks(ctx, tasks) // blocks until ctx cancelled

	if got := n.Load(); got != 1 {
		t.Fatalf("expected exactly 1 (initial) run, got %d", got)
	}
}

func TestRunTasksRepeatsOnInterval(t *testing.T) {
	var n atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	tasks := []scheduledTask{{
		name:     "t",
		interval: 10 * time.Millisecond,
		run:      func(context.Context) { n.Add(1) },
	}}

	go func() { time.Sleep(85 * time.Millisecond); cancel() }()
	runTasks(ctx, tasks)

	if got := n.Load(); got < 2 {
		t.Fatalf("expected the task to repeat on its interval, got %d runs", got)
	}
}

func TestRunTasksRespectsInitialDelay(t *testing.T) {
	var n atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	// initialDelay longer than the test window → cancelled before the first run.
	tasks := []scheduledTask{{
		name:         "t",
		interval:     time.Hour,
		initialDelay: time.Hour,
		run:          func(context.Context) { n.Add(1) },
	}}

	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	runTasks(ctx, tasks)

	if got := n.Load(); got != 0 {
		t.Fatalf("task must not run before its initialDelay elapses, got %d", got)
	}
}

func TestRunTasksConcurrentTasksAllFire(t *testing.T) {
	var a, b atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	tasks := []scheduledTask{
		{name: "a", interval: time.Hour, run: func(context.Context) { a.Add(1) }},
		{name: "b", interval: time.Hour, run: func(context.Context) { b.Add(1) }},
	}

	go func() { time.Sleep(40 * time.Millisecond); cancel() }()
	runTasks(ctx, tasks)

	if a.Load() != 1 || b.Load() != 1 {
		t.Fatalf("both tasks should run their initial pass: a=%d b=%d", a.Load(), b.Load())
	}
}

func TestEnvDurationDefaultAndOverride(t *testing.T) {
	if got := envDuration("FLEET_SCHED_NONEXISTENT_X", 42*time.Minute); got != 42*time.Minute {
		t.Fatalf("unset key should return default, got %s", got)
	}
	t.Setenv("FLEET_SCHED_TEST_INTERVAL", "90s")
	if got := envDuration("FLEET_SCHED_TEST_INTERVAL", time.Hour); got != 90*time.Second {
		t.Fatalf("override = %s, want 90s", got)
	}
	t.Setenv("FLEET_SCHED_TEST_INTERVAL", "garbage")
	if got := envDuration("FLEET_SCHED_TEST_INTERVAL", time.Hour); got != time.Hour {
		t.Fatalf("invalid value should fall back to default, got %s", got)
	}
}

// Nobody should be able to configure a one-day retention by omission.
//
// cmd/fleet-jobs rejects --purge-days below 1, but a struct literal has no such
// guard, and PurgeTelemetryPings clamps 0 up to 1 — so a caller that simply did
// not set the field would keep ONE DAY of telemetry and delete the rest on the
// first nightly run. This is the whole reason the defaults live in the package.
func TestSchedulerDepsDefaults(t *testing.T) {
	got := SchedulerDeps{}.withDefaults()
	if got.PurgeDays != DefaultPurgeDays {
		t.Fatalf("PurgeDays = %d, want %d — an unset retention must not mean one day",
			got.PurgeDays, DefaultPurgeDays)
	}
	if got.LinkDays != DefaultLinkDays {
		t.Fatalf("LinkDays = %d, want %d", got.LinkDays, DefaultLinkDays)
	}
	if got.PmWithinDays != DefaultPMWithinDays || got.PmWithinKm != DefaultPMWithinKm {
		t.Fatalf("PM lookahead = %d days / %v km, want %d / %v",
			got.PmWithinDays, got.PmWithinKm, DefaultPMWithinDays, DefaultPMWithinKm)
	}
}

// An explicit value is never overridden — the defaults fill gaps, they do not
// impose policy on a caller that stated one.
func TestSchedulerDepsKeepsExplicitValues(t *testing.T) {
	got := SchedulerDeps{PurgeDays: 30, LinkDays: 7, PmWithinDays: 3, PmWithinKm: 250}.withDefaults()
	if got.PurgeDays != 30 || got.LinkDays != 7 || got.PmWithinDays != 3 || got.PmWithinKm != 250 {
		t.Fatalf("explicit values were overwritten: %+v", got)
	}
}
