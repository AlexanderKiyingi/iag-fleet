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
DATABASE_URL=... go run ./cmd/migrate -verify    # is this build safe to deploy?
```

`-verify` compares every column the models read against the live schema and
exits non-zero listing what is missing. It is the check for the other half of
the ordering problem: `-dry-run` tells you the database is behind the
migrations, `-verify` tells you the database is behind the **binary**, which is
what takes a table down. Run it between migrating and deploying, and again if a
deploy misbehaves.

**Deploy order is: migrate, then start the API.** Running it when there is
nothing to do is a no-op, and running it twice applies nothing — versions are
recorded with a checksum.

Each file applies inside its own transaction, so a failure rolls back whole and
the schema is never left half-applied. What a failure does cost is the boot: if
`AUTO_MIGRATE` is left at its default of `true`, the API calls `os.Exit(1)` with
"auto-migrate failed; refusing to serve". Dry-run first.

### Pending as of 2026-08-28

| Version | Purpose | Shape |
|---------|---------|-------|
| `0040_vehicles_last_seen_default` | `vehicles.last_seen` had no default, so API vehicle creation was impossible | metadata only |
| `0044_form_fields_with_no_home` | Columns for fields the fleet forms already collect and the service had nowhere to put | 16 additive columns, 2 indexes |
| `0045_jmp_notes` | `jmps.notes` — missed by 0044; the journey-plan form has always had the field | 1 additive column |

> **`0045` has no model field behind it yet, deliberately.** `JMP.Notes` was
> deployed with the migration still pending and every `jmps` read 500'd with
> `column "notes" does not exist` until it was backed out. Apply `0045` first,
> then add the field back — that is the order this file has been asking for
> all along, and shipping both at once into an auto-deploying branch is how
> it gets broken.

All three are additive and guarded with `IF NOT EXISTS`, so they apply to a populated
database without touching a row. `0044`'s DATE columns are deliberately nullable:
the generic CRUD resource inserts every column and an empty string cast to date
writes NULL, so a NOT NULL date with no value to supply would make creation
impossible — the same failure `0040` fixes for `vehicles.last_seen`.

**Until `0044` is applied the columns do not exist.** The service omits them from
its JSON, the app reads them empty, and the app's own contract suite
(`npm run iag:contract` in the iag-fleet frontend) fails vehicle, driver and trip
with `[needs fleet migration 0044]`. That red IS the check: it goes green when
the migration has run, and it is how to confirm the deploy took.

Deploying the API before the migration is what broke production once already —
`7873109` reverted the vehicle-lifecycle and driver-authorisation work
("vehicles was 502 in production") because the model gained columns the database
did not have. **Migrate first, then start the API.** That revert is also why
`/api/vehicle-categories`, `/api/permit-classes` and `/api/permit-authorisations`
still 404: the handlers, router entries and migrations were removed together and
the frontend adapters for them were not. The code is recoverable from `7873109^`
when the deploy order can be guaranteed.

Numbers `0041`–`0043` were taken by other in-flight work after the revert freed
them, which is why this file jumps to `0044`.
