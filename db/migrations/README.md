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
