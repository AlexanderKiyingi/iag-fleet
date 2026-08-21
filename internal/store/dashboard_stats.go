package store

import (
	"context"
	"time"
)

// Dashboard aggregates.
//
// The command-centre summary used to be assembled by loading nine whole tables
// into Go and counting in loops — vehicles, JMPs, requests, cargo, fuel,
// compliance (twice), safety, drivers and maintenance — to produce about
// fifteen integers and a short alert feed. Every one of those reads was
// unbounded, so the page got slower every month, and the whole thing ran on
// every request because the cache in front of it needs a Redis that is not
// provisioned yet.
//
// Postgres can count. The counters below are one round trip; the alert feed is
// one bounded, indexed query per source instead of a full-table scan plus a
// filter in Go. Display names come from LEFT JOINs rather than from loading the
// entire driver and vehicle tables to build lookup maps.

// alertsPerSource bounds each contributing query. The feed is a triage list
// that gets sorted and read from the top — an operator does not scroll 4,000
// expired documents, and shipping them costs a scan and a payload on every
// dashboard load. Sources report whether they were truncated so the UI can say
// "showing first N".
const alertsPerSource = 50

// DashboardCounters is every scalar on the dashboard KPI strip.
type DashboardCounters struct {
	VehiclesTotal      int
	Moving             int
	OfflineOrMx        int
	JmpsActive         int
	JmpsPending        int
	RequestsOpen       int
	FuelMtdUgx         float64
	FuelAnomalies      int
	ComplianceAtRisk   int
	MaintenanceOverdue int
}

// DashboardCounters runs the whole KPI strip as a single query.
//
// Scalar subqueries rather than a join: each one is independently indexed
// (vehicles.status, jmps.status, compliance_status_idx, maintenance_status_idx,
// fuel_records_date_idx and the partial fuel_records_anomaly_idx all exist), and
// a join across nine unrelated tables would multiply rows for no reason.
//
// Dates are compared in UTC to match what the API hands out — the model's date
// fields are rendered as UTC 'YYYY-MM-DD' strings, so a server running in
// another zone must not shift the month boundary underneath them.
func (r *Repository) DashboardCounters(ctx context.Context) (DashboardCounters, error) {
	const q = `
        WITH today AS (SELECT (NOW() AT TIME ZONE 'UTC')::date AS d)
        SELECT
            (SELECT COUNT(*) FROM vehicles),
            (SELECT COUNT(*) FROM vehicles WHERE status = 'moving'),
            (SELECT COUNT(*) FROM vehicles WHERE status IN ('offline', 'maintenance')),
            (SELECT COUNT(*) FROM jmps WHERE status = 'active'),
            (SELECT COUNT(*) FROM jmps WHERE status = 'pending-toolbox'),
            (SELECT COUNT(*) FROM service_requests WHERE status IN ('submitted', 'reviewed')),
            (SELECT COALESCE(SUM(total), 0) FROM fuel_records
               WHERE date >= date_trunc('month', (SELECT d FROM today))::date),
            (SELECT COUNT(*) FROM fuel_records WHERE anomaly),
            (SELECT COUNT(*) FROM compliance_items WHERE status <> 'valid'),
            (SELECT COUNT(*) FROM maintenance_items
               WHERE status IN ('scheduled', 'overdue') AND date < (SELECT d FROM today))`
	var out DashboardCounters
	err := r.pool.QueryRow(ctx, q).Scan(
		&out.VehiclesTotal, &out.Moving, &out.OfflineOrMx,
		&out.JmpsActive, &out.JmpsPending, &out.RequestsOpen,
		&out.FuelMtdUgx, &out.FuelAnomalies,
		&out.ComplianceAtRisk, &out.MaintenanceOverdue,
	)
	return out, err
}

// CargoStageCounts returns cargo row counts keyed by stage.
func (r *Repository) CargoStageCounts(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT stage, COUNT(*) FROM cargo GROUP BY stage`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var stage string
		var n int
		if err := rows.Scan(&stage, &n); err != nil {
			return nil, err
		}
		out[stage] = n
	}
	return out, rows.Err()
}

// DashboardAlert is one row of the triage feed, already carrying the display
// name its source resolved via LEFT JOIN.
type DashboardAlert struct {
	Severity string
	Title    string
	Detail   string
	When     string
	Href     string
}

// DashboardAlerts collects the alert feed from every source.
//
// Each source is capped at alertsPerSource; truncated reports which sources hit
// the cap so the caller can say so rather than quietly showing a partial list.
func (r *Repository) DashboardAlerts(ctx context.Context) (alerts []DashboardAlert, truncated []string, err error) {
	type source struct {
		name string
		q    string
		args []any
	}
	utcDay := time.Now().UTC().Format("2006-01-02")

	sources := []source{
		{
			// Expired and missing documents, then expiring ones. Both read the
			// same rows, so they are one query with the severity decided in SQL.
			name: "compliance",
			q: `
                SELECT CASE WHEN ci.status = 'expiring' THEN 'warn' ELSE 'crit' END,
                       ci.doc_type || ' ' || CASE WHEN ci.status = 'expiring' THEN 'expiring' ELSE ci.status END,
                       COALESCE(NULLIF(d.name, ''), NULLIF(v.plate, ''),
                                NULLIF(ci.driver_id, ''), NULLIF(ci.vehicle_id, ''), '—'),
                       COALESCE(to_char(ci.expiry, 'YYYY-MM-DD'), ''),
                       '/compliance'
                FROM compliance_items ci
                LEFT JOIN drivers  d ON d.id = ci.driver_id
                LEFT JOIN vehicles v ON v.id = ci.vehicle_id
                WHERE ci.status IN ('expired', 'missing', 'expiring')
                ORDER BY (ci.status = 'expiring'), ci.expiry DESC NULLS LAST
                LIMIT $1`,
			args: []any{alertsPerSource},
		},
		{
			name: "maintenance",
			q: `
                SELECT 'crit',
                       'WO overdue · ' || m.type,
                       CASE WHEN COALESCE(v.plate, '') <> '' THEN v.plate || ' · ' || m.service ELSE m.service END,
                       to_char(m.date, 'YYYY-MM-DD'),
                       '/maintenance'
                FROM maintenance_items m
                LEFT JOIN vehicles v ON v.id = m.vehicle_id
                WHERE m.status IN ('scheduled', 'overdue') AND m.date < $2::date
                ORDER BY m.date DESC
                LIMIT $1`,
			args: []any{alertsPerSource, utcDay},
		},
		{
			name: "safety",
			q: `
                SELECT 'crit',
                       'Safety · ' || s.type,
                       COALESCE(NULLIF(v.plate, ''), '—'),
                       to_char(s.date AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
                       '/safety'
                FROM safety_events s
                LEFT JOIN vehicles v ON v.id = s.vehicle_id
                WHERE s.severity = 'crit' AND s.status IN ('open', 'investigating')
                ORDER BY s.date DESC
                LIMIT $1`,
			args: []any{alertsPerSource},
		},
		{
			name: "requests",
			q: `
                SELECT 'warn', 'New vehicle request', COALESCE(requester_dept, ''),
                       COALESCE(to_char(submitted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'), ''),
                       '/requests/' || id
                FROM service_requests
                WHERE status = 'submitted'
                ORDER BY submitted_at DESC NULLS LAST
                LIMIT $1`,
			args: []any{alertsPerSource},
		},
		{
			// Deliberately 3, matching the previous behaviour: fuel anomalies
			// are a nudge toward the fuel page, not the page itself.
			name: "fuel",
			q: `
                SELECT 'warn', 'Fuel anomaly',
                       COALESCE(NULLIF(v.plate, ''), '?') ||
                         CASE WHEN COALESCE(f.anomaly_reason, '') <> ''
                              THEN ' · ' || f.anomaly_reason ELSE '' END,
                       to_char(f.date, 'YYYY-MM-DD'),
                       '/fuel'
                FROM fuel_records f
                LEFT JOIN vehicles v ON v.id = f.vehicle_id
                WHERE f.anomaly
                ORDER BY f.date DESC
                LIMIT $1`,
			args: []any{3},
		},
		{
			name: "cargo",
			q: `
                SELECT 'warn', 'Cargo at ACP · awaiting offload',
                       COALESCE(truck_plate, ''),
                       COALESCE(to_char(arrival_acp, 'YYYY-MM-DD'), ''),
                       '/cargo/' || id
                FROM cargo
                WHERE stage = 'at-acp'
                ORDER BY arrival_acp DESC NULLS LAST
                LIMIT $1`,
			args: []any{alertsPerSource},
		},
	}

	for _, src := range sources {
		rows, qerr := r.pool.Query(ctx, src.q, src.args...)
		if qerr != nil {
			return nil, nil, qerr
		}
		n := 0
		for rows.Next() {
			var a DashboardAlert
			if serr := rows.Scan(&a.Severity, &a.Title, &a.Detail, &a.When, &a.Href); serr != nil {
				rows.Close()
				return nil, nil, serr
			}
			alerts = append(alerts, a)
			n++
		}
		rerr := rows.Err()
		rows.Close()
		if rerr != nil {
			return nil, nil, rerr
		}
		if n >= alertsPerSource {
			truncated = append(truncated, src.name)
		}
	}
	return alerts, truncated, nil
}
