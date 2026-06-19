package main

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
