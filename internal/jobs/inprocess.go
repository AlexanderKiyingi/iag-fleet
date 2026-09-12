package jobs

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// schedulerLockKey is an arbitrary but fixed advisory-lock id for "the fleet
// maintenance scheduler". Any constant works as long as nothing else in the
// database picks the same one; this is derived from the words rather than a
// round number so a collision with some other feature's ad-hoc lock is
// unlikely.
const schedulerLockKey int64 = 0x1A6F_1EE7_0B5C_0001

// StartInProcess runs the maintenance scheduler inside the API process, if and
// only if this replica wins an advisory lock.
//
// The scheduler is meant to be a single long-lived worker deployed alongside
// the API. It was not deployed at all: telemetry_daily held zero rows, so no
// rollups existed, no retention ran, and no partitions were pre-created — while
// raw pings accumulated. Standing up a separate worker is a deployment change;
// this is the same job, opt-in, with nothing new to provision.
//
// The lock is what makes that safe. Every API replica calls this, and exactly
// one gets the lock and runs the jobs; the rest return immediately. Without it
// three replicas would each aggregate the same day, each purge, and each detect
// the same trips.
//
// The lock is held on its own connection for the life of the process. Postgres
// releases it automatically when that connection drops, so a replica that dies
// hands the work to whichever one next restarts rather than wedging the
// scheduler until someone intervenes.
func StartInProcess(ctx context.Context, pool *pgxpool.Pool, deps SchedulerDeps) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		slog.Error("in-process scheduler: could not acquire a connection for the lock", "err", err)
		return
	}

	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, schedulerLockKey).Scan(&got); err != nil {
		slog.Error("in-process scheduler: advisory lock failed", "err", err)
		conn.Release()
		return
	}
	if !got {
		// Another replica is running the jobs. Not a problem, and not worth a
		// warning — this is the expected answer for every replica but one.
		slog.Info("in-process scheduler: another replica holds the scheduler lock; standing down")
		conn.Release()
		return
	}

	slog.Info("in-process scheduler: holding the scheduler lock, running maintenance jobs in this process")
	go func() {
		// Release only when the process is shutting down; holding the
		// connection is what holds the lock.
		defer conn.Release()
		RunScheduler(ctx, deps)
		slog.Info("in-process scheduler: stopped")
	}()
}
