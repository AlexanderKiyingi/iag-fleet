package handlers

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TelemetryLocation is where one connection actually finds the pings table.
type TelemetryLocation struct {
	Pool       string `json:"pool"`
	Database   string `json:"database"`
	SearchPath string `json:"searchPath"`
	Resolved   string `json:"resolvedSchema"`
	// Schemas that also hold a table of this name, so a shadow copy is visible.
	AlsoIn []string `json:"alsoIn,omitempty"`
	// Planner estimate, not count(*) — this is a hypertable and the exact
	// number matters far less than "millions" versus "none".
	ApproxRows int64  `json:"approxRows"`
	Error      string `json:"error,omitempty"`
}

/*
DescribeTelemetryLocation answers, for one pool, the question that cost a day:
which physical table do reads and writes on this connection actually hit.

The fault it exists for produced no error on either side. The gateway wrote
pings and logged success; this service read them and returned an empty array;
both were talking to a table called telemetry_timeseries, and they were not the
same table. Every check in the stack passed — the schema-drift check looks for
missing COLUMNS, and the columns were all present, on the wrong relation.

Reported per pool because this service holds two, and they need not agree: the
operational pool is where migrations run and where the table gets CREATED, while
the telemetry pool is where pings are READ. A split between those two is
invisible to every other diagnostic here.
*/
func DescribeTelemetryLocation(ctx context.Context, name string, pool *pgxpool.Pool, table string) TelemetryLocation {
	loc := TelemetryLocation{Pool: name}
	if pool == nil {
		loc.Error = "pool not configured"
		return loc
	}
	if err := pool.QueryRow(ctx, `SELECT current_database(), current_setting('search_path')`).
		Scan(&loc.Database, &loc.SearchPath); err != nil {
		loc.Error = fmt.Sprintf("identify connection: %v", err)
		return loc
	}

	// to_regclass follows the live search_path — the only thing that decides
	// where an unqualified read or write lands.
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(n.nspname, ''), COALESCE(c.reltuples, -1)::bigint
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.oid = to_regclass($1)`, table).Scan(&loc.Resolved, &loc.ApproxRows); err != nil {
		loc.Error = fmt.Sprintf("resolve %s: %v", table, err)
		return loc
	}

	rows, err := pool.Query(ctx, `
		SELECT table_schema FROM information_schema.tables
		WHERE table_name = $1 AND table_schema <> $2 ORDER BY table_schema`, table, loc.Resolved)
	if err != nil {
		loc.Error = fmt.Sprintf("find shadow copies: %v", err)
		return loc
	}
	defer rows.Close()
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			loc.Error = err.Error()
			return loc
		}
		loc.AlsoIn = append(loc.AlsoIn, schema)
	}
	return loc
}
