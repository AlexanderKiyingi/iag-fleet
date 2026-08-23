# Fleet SQL migrations

Migrations are **immutable** once applied (`schema_migrations` stores a sha256 checksum).

## Telemetry table history

| Version | Purpose |
|---------|---------|
| `0001_initial` | Creates legacy `telemetry_pings` (unchanged for checksum compatibility) |
| `0010_telemetry_timeseries` | Creates `telemetry_timeseries`, hypertable, migrates from `telemetry_pings`, drops legacy table |
| `0011_telemetry_timeseries_no_id` | Drops synthetic `id` column (Timescale-friendly) |
| `0012_timescale_existing_volume` | `CREATE EXTENSION timescaledb` + hypertable on upgraded Postgres volumes |

Fresh `go run ./cmd/seed --reset` still runs `0001` then `0010+`; the legacy table exists only briefly during migrate. Do **not** edit `0001_initial.sql` on deployed environments — add a new numbered file instead.

## Applying pending migrations

Production sets `ENVIRONMENT=production`, and `config.Validate` then refuses to
start the API unless `AUTO_MIGRATE=false` — several replicas racing to migrate
on boot is how a schema ends up half-applied. So in production the API never
migrates; `cmd/migrate` does, out of band.

```bash
DATABASE_URL=... go run ./cmd/migrate -dry-run   # report, change nothing
DATABASE_URL=... go run ./cmd/migrate            # apply
```

**Deploy order is: migrate, then start the API.** Running it when there is
nothing to do is a no-op, and running it twice applies nothing — versions are
recorded with a checksum.

Each file applies inside its own transaction, so a failure rolls back whole and
the schema is never left half-applied. What a failure does cost is the boot: if
`AUTO_MIGRATE` is left at its default of `true`, the API calls `os.Exit(1)` with
"auto-migrate failed; refusing to serve". Dry-run first.

### Pending as of 2026-08-23

| Version | Purpose | Shape |
|---------|---------|-------|
| `0040_vehicles_last_seen_default` | `vehicles.last_seen` had no default, so API vehicle creation was impossible | metadata only |
| `0041_vehicle_lifecycle_state` | Asset lifecycle (FR-VEH-06) + disposal detail (FR-VEH-10) | 8 additive columns, CHECK, index |
| `0042_driver_authorisation_matrix` | Driver-vehicle authorisation matrix (FR-DRV-04) | 3 new tables + `vehicles.category_id` |

All three are additive and guarded with `IF NOT EXISTS`. `0041`'s CHECK
constraint passes trivially because the column it validates is created in the
same statement with `DEFAULT 'Active'`, so every existing row satisfies it.
`0042` seeds no rows on purpose — the authorisation check fails open on an empty
matrix, and a seeded taxonomy would start refusing assignments on the day it
deployed.

Until these are applied, `lifecycle_state`, `category_id` and the three matrix
tables do not exist: the lifecycle transition endpoint and the
`/api/vehicle-categories`, `/api/permit-classes` and `/api/permit-authorisations`
collections will error against the running service.
