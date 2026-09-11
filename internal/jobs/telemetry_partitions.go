package jobs

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
Partition maintenance for telemetry_timeseries.

PostgreSQL does not create range partitions on demand, and pg_partman is not
available on the deployed server, so something has to make next month's
partition before next month's first ping. That is this.

It is deliberately not the only thing standing between a ping and an error: the
table also has a DEFAULT partition, so a month nobody pre-created still accepts
writes. Losing telemetry because a cron did not fire would be a far worse
failure than a few rows landing in the default and being tidied up later —
especially since a failed INSERT stops the live map too, as Pipeline.Ingest
returns before the hot-state update.
*/

// MonthsAhead is how far in front of today partitions are kept.
//
// Twelve, because the cost is an empty table per month and the failure mode at
// the other end is telemetry piling into the default partition where retention
// cannot drop it cleanly.
const MonthsAhead = 12

// EnsureTelemetryPartitions creates any missing monthly partitions from the
// current month to MonthsAhead. Idempotent: existing months are left alone.
func EnsureTelemetryPartitions(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	schema, partitioned, err := telemetryTableShape(ctx, pool)
	if err != nil {
		return 0, err
	}
	if schema == "" || !partitioned {
		// Plain table or hypertable: nothing here owns its layout.
		return 0, nil
	}

	created := 0
	month := time.Now().UTC().Truncate(24 * time.Hour)
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i <= MonthsAhead; i++ {
		start := month.AddDate(0, i, 0)
		end := start.AddDate(0, 1, 0)
		name := fmt.Sprintf("telemetry_timeseries_%04d_%02d", start.Year(), int(start.Month()))
		tag, err := pool.Exec(ctx, fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %q.%q PARTITION OF %q.telemetry_timeseries
			   FOR VALUES FROM ('%s') TO ('%s')`,
			schema, name, schema,
			start.Format("2006-01-02"), end.Format("2006-01-02")))
		if err != nil {
			return created, fmt.Errorf("create partition %s: %w", name, err)
		}
		_ = tag
		created++
	}
	return created, nil
}

/*
DropTelemetryPartitionsBefore removes whole months older than the cutoff.

This is the reason for partitioning. PurgeTelemetryPings deletes rows, which on
an append-only table means a long transaction and a large amount of bloat left
for autovacuum — on a database twenty-one other schemas share. Dropping a month
is instant and leaves nothing behind.

Only partitions that end at or before the cutoff are dropped, so a month that
still holds rows newer than the retention window is never touched. The DEFAULT
partition is never dropped: it is not bounded by time, so it may hold recent
rows, and dropping it would also remove the safety net that keeps ingest alive
when a month is missing.
*/
func DropTelemetryPartitionsBefore(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time) ([]string, error) {
	schema, partitioned, err := telemetryTableShape(ctx, pool)
	if err != nil || schema == "" || !partitioned {
		return nil, err
	}

	rows, err := pool.Query(ctx, `
		SELECT c.relname,
		       pg_get_expr(c.relpartbound, c.oid)
		  FROM pg_class parent
		  JOIN pg_inherits i ON i.inhparent = parent.oid
		  JOIN pg_class c ON c.oid = i.inhrelid
		  JOIN pg_namespace n ON n.oid = parent.relnamespace
		 WHERE parent.relname = 'telemetry_timeseries' AND n.nspname = $1`, schema)
	if err != nil {
		return nil, err
	}
	type part struct{ name, bound string }
	var parts []part
	for rows.Next() {
		var p part
		if err := rows.Scan(&p.name, &p.bound); err != nil {
			rows.Close()
			return nil, err
		}
		parts = append(parts, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var dropped []string
	for _, p := range parts {
		end, ok := partitionEnd(p.bound)
		if !ok {
			continue // DEFAULT, or a bound this does not understand — leave it
		}
		if !end.After(cutoff) {
			if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP TABLE %q.%q`, schema, p.name)); err != nil {
				return dropped, fmt.Errorf("drop partition %s: %w", p.name, err)
			}
			dropped = append(dropped, p.name)
		}
	}
	return dropped, nil
}

func telemetryTableShape(ctx context.Context, pool *pgxpool.Pool) (schema string, partitioned bool, err error) {
	err = pool.QueryRow(ctx, `
		SELECT n.nspname, c.relkind = 'p'
		  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE c.oid = to_regclass('telemetry_timeseries')`).Scan(&schema, &partitioned)
	if err != nil {
		return "", false, nil // absent on this connection; not this job's problem
	}
	return schema, partitioned, nil
}

/*
partitionEnd reads the upper bound out of a partition's bound expression.

Postgres renders these as

	FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00')

and there is no catalogue column holding the parsed value, so the text is what
there is to work with.

Returns ok=false for anything it does not recognise — DEFAULT most of all.
Every caller treats that as "leave this partition alone", which is the only safe
reading: a bound this cannot parse is a bound whose contents it cannot reason
about, and dropping a table on a guess is not a recoverable mistake.
*/
func partitionEnd(bound string) (time.Time, bool) {
	m := partitionToRe.FindStringSubmatch(bound)
	if len(m) != 2 {
		return time.Time{}, false
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05-07",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, m[1]); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

var partitionToRe = regexp.MustCompile(`TO \('([^']+)'\)`)
