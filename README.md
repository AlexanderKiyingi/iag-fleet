# HAULA Fleet — Go/Gin backend

REST API that mirrors the data shapes of the Next.js frontend (`lib/types.ts`
and `lib/store.tsx`). **Every layer is Postgres-backed** — domain entities
(vehicles, drivers, jmps, cargo, fuel records, etc.), authentication,
sessions, RBAC, IoT telemetry, audit log, operator ticker.

`DATABASE_URL` is mandatory; the API exits at startup if it isn't set.

The 15 fleet entities live in regular relational tables. Date and timestamp
columns are cast to text on SELECT (`to_char`) so the JSON shape the frontend
consumes is identical to what the legacy localStorage store produced — no
client-side changes needed.

## Run

```sh
cd backend
go mod tidy

# 1) Apply schema + seed (one-time)
DATABASE_URL=postgres://postgres:postgres@localhost:5432/haula_fleet \
    go run ./cmd/seed --reset

# 2) Boot the API
DATABASE_URL=postgres://postgres:postgres@localhost:5432/haula_fleet go run .
```

Defaults: listens on `:8080`, CORS allows `http://localhost:3000`, session
cookies are not marked `Secure` (dev mode). Env vars:

| Var            | Effect                                                            |
| -------------- | ----------------------------------------------------------------- |
| `DATABASE_URL` | Postgres DSN. **Required.** The API refuses to start without it — every entity, including auth and audit, lives in Postgres. |
| `ADDR`         | Bind address (default `:8080`).                                    |
| `CORS_ORIGIN`  | Allowed origin for credentialed CORS (default `http://localhost:3000`). |
| `COOKIE_SECURE`| `true` to set `Secure` on the session cookie. Required when serving over HTTPS / cross-site. |
| `APP_NAME` / `APP_URL` | Branding + base URL embedded in email links (default `HAULA Fleet` / `http://localhost:3000`). |
| `REQUIRE_EMAIL_VERIFIED` | When `true` (default), unverified users can't log in. Staff/superusers always bypass. |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS` / `SMTP_FROM` | SMTP credentials. **Leave `SMTP_HOST` blank to use the LogMailer** — emails print to stdout, which is great in dev to see the verification/reset links. |
| `REDIS_URL` | Optional `redis://host:6379/0` (or `rediss://` for TLS). When set and reachable, JSON responses for `/api/dashboard/summary`, `/api/analytics/summary`, `/api/reference`, and `/api/reference/geo` are cached with short TTLs. If unset or the dial fails, the API behaves exactly as before ([`internal/cache`](./internal/cache) uses a no-op implementation). |
| `CACHE_TTL_DASHBOARD_SEC` | Dashboard cache TTL in seconds (default **30**). |
| `CACHE_TTL_ANALYTICS_SEC` | Analytics cache TTL in seconds (default **45**). |
| `CACHE_TTL_REFERENCE_SEC` | Reference/geo cache TTL in seconds (default **600**). |

**Cache invalidation:** successful `POST /api/admin/import` and `POST /api/admin/reset` delete the aggregate keys so the next read recomputes from Postgres.

Health check: `GET /healthz` (no auth).

## Layout

```
backend/
├── main.go                   # entrypoint
├── go.mod
├── cmd/
│   └── seed/                 # `go run ./cmd/seed` — applies schema + seed
├── db/
│   ├── schema.sql            # Postgres DDL for every entity
│   ├── seed.sql              # seed data mirroring lib/data/*
│   └── reset.sql             # destructive teardown (used by --reset)
└── internal/
    ├── models/               # domain types with `db` tags + JSONB Scanner/Valuer
    ├── store/                # generic Postgres-backed Collection[T] (reflection-driven SQL)
    ├── auth/                 # User/Group/Permission models, password hash, sessions, tokens, middleware
    ├── db/                   # pgx pool factory
    ├── mail/                 # Mailer interface + SMTPMailer + LogMailer + embedded templates
    ├── iot/                  # Codec 8/8E parser, telemetry store, in-process SSE broker
    ├── cache/                # Optional Redis response cache ([cache.NoOp] when REDIS_URL unset)
    ├── jobs/                 # Shared batch tasks (telemetry aggregate + purge) for CLI entrypoints
    ├── handlers/             # CRUD + admin + auth + users + workflows + iot + fleet live SSE
    └── router/               # Gin route wiring + CORS + auth middleware
```

Additional binaries (under `cmd/`):

| Binary               | What it does                                                       |
| -------------------- | ------------------------------------------------------------------ |
| `seed`               | Apply schema + seed (one-time / reset)                             |
| `createsuperuser`    | Django-style account creation                                      |
| `iot-gateway`        | Native Teltonika Codec 8/8E **TCP** listener (default `:5027`)     |
| `telemetry-aggregate`| Rolls raw pings into `telemetry_daily` (intended for nightly cron, just after midnight UTC) |
| `telemetry-purge`    | Drops telemetry pings older than `--days` (intended for nightly cron) |
| `fleet-jobs`         | Runs aggregate and/or purge in one invocation (`--all`, `--aggregate`, `--purge`) for cron/Kubernetes |

## Database (Postgres)

The schema and seed data live in [db/](./db). The seed runner connects via the
`DATABASE_URL` env var (`postgres://user:pass@host:port/db`). Copy
`.env.example` to `.env` and edit as needed.

```sh
# create the database first
createdb haula_fleet

# fresh setup: drop any existing tables, apply schema, load seed
DATABASE_URL=postgres://postgres:postgres@localhost:5432/haula_fleet \
    go run ./cmd/seed --reset

# re-seed without dropping (idempotent — ON CONFLICT DO NOTHING)
DATABASE_URL=... go run ./cmd/seed --seed-only

# schema only (no seed inserts)
DATABASE_URL=... go run ./cmd/seed --schema-only

# point at SQL dir explicitly (defaults to ./db or ./backend/db)
DATABASE_URL=... go run ./cmd/seed --dir /path/to/db
```

After a successful run the seed tool prints a row-count summary so you can
confirm each table populated.

### Migrations

Schema lives in `db/migrations/000N_<description>.sql`, applied by [`internal/migrate`](./internal/migrate/migrate.go). Each file is recorded in `schema_migrations` with a sha256 checksum; editing an already-applied migration is a startup error (create a new numbered file instead).

```sh
DATABASE_URL=... go run ./cmd/seed                # apply pending migrations + load seed.sql
DATABASE_URL=... go run ./cmd/seed --reset        # drop everything, migrate from scratch, load seed
DATABASE_URL=... go run ./cmd/seed --schema-only  # migrations only — production deploys
```

The migration runner wraps each file in a transaction; **don't add `BEGIN`/`COMMIT` to the file body**. SQL files are embedded into the binary via `embed.FS` so deployments ship as a single artifact.

### Schema notes

- Most cross-entity references are plain `TEXT` columns rather than foreign
  keys. The frontend seed contains dangling references (vehicles point at
  drivers that aren't seeded; one service request points at a JMP that isn't
  seeded). Adding strict FKs would block the initial load. The deliberate
  exception is `deployment_entries.deployment_day_id`, which is consistent.
- `jmps.toolbox`, `cargo.stage_history`, and `task_items.links` are stored as
  `JSONB` to mirror the nested TS shapes. `jmps.parking_photos` uses
  Postgres `TEXT[]`.
- Dynamic timestamps in [seed.sql](./db/seed.sql) use `NOW() - INTERVAL '...'`
  expressions, so the seed always looks "recent" relative to whenever it runs.
- Deployment entries are normalized into their own table rather than nested
  JSONB on `deployment_days` — easier to query and to update one entry at a
  time.

## Routes

Each resource is exposed at `/api/<resource>` with the same five verbs:

| Method | Path                      | Description                          |
| ------ | ------------------------- | ------------------------------------ |
| GET    | `/api/<resource>`         | list                                 |
| GET    | `/api/<resource>/:id`     | fetch one                            |
| POST   | `/api/<resource>`         | create (server fills `id` if blank)  |
| PUT    | `/api/<resource>/:id`     | full replace                         |
| PATCH  | `/api/<resource>/:id`     | partial update (JSON-merge)          |
| DELETE | `/api/<resource>/:id`     | remove                               |

Resources: `vehicles`, `drivers`, `jmps`, `cargo`, `cargo-docs`, `fuel`,
`maintenance`, `parts`, `tyres`, `trips`, `safety`, `compliance`, `requests`,
`tasks`, `deployment`.

Non-CRUD endpoints:

| Method | Path                  | Description                                |
| ------ | --------------------- | ------------------------------------------ |
| GET    | `/api/ticker`         | operator ticker (diesel, ugx, name, role)  |
| PATCH  | `/api/ticker`         | update non-zero fields                     |
| GET    | `/api/audit`          | newest-first audit log (capped at 500)     |
| GET    | `/api/audit/search`   | filterable + paginated audit log           |
| GET    | `/api/admin/export`   | full snapshot in the frontend's shape      |
| POST   | `/api/admin/import`   | replace collections from a snapshot        |
| POST   | `/api/admin/reset`    | clear all collections                      |

Reference data (no auth, cacheable):

| Method | Path                    | Description                                          |
| ------ | ----------------------- | ---------------------------------------------------- |
| GET    | `/api/reference`        | enums: departments, safety/doc types, statuses, etc. |
| GET    | `/api/reference/geo`    | POIs, corridors, basemaps                            |
| GET    | `/api/dashboard/summary`| KPIs, cargo pipeline, ranked alert feed              |

Workflows (multi-field / cross-entity transitions; logged to audit):

| Method | Path                                            | Behaviour                                                                          |
| ------ | ----------------------------------------------- | ---------------------------------------------------------------------------------- |
| POST   | `/api/jmps/:id/complete-toolbox`                | force all 8 toolbox items checked, mark complete, transition `pending-toolbox→active` |
| POST   | `/api/jmps/:id/cancel`                          | set status `cancelled`                                                             |
| POST   | `/api/jmps/:id/approve-mileage` `{approved,notes}` | stamp `mileageStatus`, `approvedBy`, `approvedAt`                              |
| POST   | `/api/cargo/:id/set-stage` `{stage,note?}`      | jump to stage, append history event, auto-set `arrivalAcp` on `at-acp`             |
| POST   | `/api/cargo/:id/advance-stage`                  | move to the next stage in `CargoStages` order                                      |
| POST   | `/api/cargo/:id/offload`                        | terminal `offloaded` + stamp `offloadingDate`                                      |
| POST   | `/api/cargo/:id/demobilise`                     | terminal `demobilised` + `demobilisedAt`                                           |
| POST   | `/api/requests/:id/assign` `{vehicleId,driverId,reviewerNotes?}` | guarded assignment; if `taskId` is linked, that task moves to `in-progress` |
| POST   | `/api/tasks/:id/complete`                       | set state `done` + `completedAt`                                                   |
| POST   | `/api/deployment/seed-today`                    | create today's `DeploymentDay`; pre-fills entries for all vehicles, carrying ODO from prior day |
| POST   | `/api/deployment/:id/entries` `{vehicleId,...}` | append entry; bumps vehicle ODO upward if `odoEnd` is greater                      |
| POST   | `/api/vehicles/simulate-tick`                   | one simulator step: every `moving` vehicle steps along its heading (bulk update)   |

## Authentication & RBAC

Modeled on Django's `auth.User` / `auth.Group` / `auth.Permission`.

- **Users** have `is_active`, `is_staff`, `is_superuser` flags. Superusers bypass all permission checks.
- **Groups** carry permissions; users inherit their groups' permissions.
- **Permissions** use Django-style `<action>_<entity>` codenames (`view_vehicle`, `change_jmp`, `delete_part`) plus custom workflow codenames (`approve_mileage_jmp`, `assign_request`, `seed_deployment`, `simulate_vehicles`, `export_data`, `reset_data`, ...).
- **Sessions** are server-side rows in `auth_sessions`. Login returns an `HttpOnly` cookie; logout deletes the row and clears the cookie. TTL is 14 days; expired rows are pruned in-line on access.

**Session revocation rules** (mirror common security best-practice):
- Self password change (`/api/auth/me/password`): every other session for the user is killed; the caller stays logged in.
- Token-driven reset (`/api/auth/reset-password`) and admin reset (`/api/users/:id/password`): **every** session for the target user is killed; they must log in fresh.
- Admin deactivation (`PATCH /api/users/:id` with `isActive=false`): every session is killed immediately. (The middleware also clears stale sessions lazily on next access if for some reason this is missed.)

### Default seed accounts and groups

The seed (`db/seed.sql`) creates four groups and one default user:

| Group         | Permissions                                                                |
| ------------- | -------------------------------------------------------------------------- |
| `admin`       | Every permission.                                                          |
| `fleet-manager` | Everything except `import_data` and `reset_data` (destructive admin).    |
| `dispatcher`  | All `view_*` + change/add/assign on requests, tasks, deployments, JMP toolbox, cargo stage. |
| `viewer`      | Read-only — every `view_*` codename.                                       |

**Login is by email**, not username. Username remains as a display handle (audit logs, profile rendering).

| Email                 | Password | Username (display) | Group           | Flags                                                  | Use case |
| --------------------- | -------- | ------------------ | --------------- | ------------------------------------------------------ | -------- |
| `[email protected]` | `admin`  | `admin`            | `admin`         | `is_active`, `is_staff`, `is_superuser` = true         | System administration; bypasses all permission checks |
| `[email protected]`  | `demo`   | `demo`             | `fleet-manager` | `is_active=true`; `is_staff` and `is_superuser` = false | Tour-the-app account: full operational CRUD across the fleet domain, but cannot reach `/users` / `/groups` / `/iot-devices` admin pages and cannot reset/import the dataset |

Both accounts ship with `email_verified=true` so login works without an SMTP relay configured.

#### If the seeded credentials don't work

The seed only runs when you invoke it explicitly — it doesn't run on
container startup. After deploying a fresh database (or after pulling
this repo on top of an older deployment) run:

```sh
DATABASE_URL=... go run ./cmd/seed
```

The `demo` user is **self-healing**: re-running the seed always resets
its email, password hash, `email_verified`, and `is_active` back to
the documented values via `ON CONFLICT (username) DO UPDATE`. So if
the documented `demo / [email protected]` credentials don't work,
re-running the seed will make them work — no manual SQL needed.

The `admin` user uses `ON CONFLICT DO NOTHING` instead, because
deployers may have rotated its password to something stronger and we
shouldn't clobber that. If you've never changed the admin password
and the seeded one isn't working either, that means admin's row pre-
dates the email-login change — there's a backfill `UPDATE users SET
email='[email protected]' WHERE username='admin' AND email IS NULL` in
the same seed run that fills in the missing email, after which login
works.

> **Change both default passwords before exposing this anywhere.** Three options:
>
> ```sh
> # 1) Django-style CLI: create or rotate a superuser
> DATABASE_URL=... go run ./cmd/createsuperuser \
>     --username admin --password 'something-strong'
>
> # 2) Self-service after login
> curl -b cookies -c cookies -X POST /api/auth/me/password \
>     -d '{"currentPassword":"admin","newPassword":"something-strong"}'
>
> # 3) Admin-reset another user
> curl -b cookies -X POST /api/users/42/password \
>     -d '{"newPassword":"something-strong"}'
> ```

### Auth endpoints

| Method | Path                              | Description                                      |
| ------ | --------------------------------- | ------------------------------------------------ |
| POST   | `/api/auth/login`                 | `{username, password}` → sets session cookie + returns user with permissions. Returns 403 `{error:"email_not_verified"}` if `REQUIRE_EMAIL_VERIFIED=true` and the user has an email on file but hasn't verified it. Users without an email and staff/superusers always bypass the gate. |
| POST   | `/api/auth/logout`                | revokes the session, clears the cookie           |
| GET    | `/api/auth/me`                    | current user + groups + resolved permissions     |
| POST   | `/api/auth/me/password`           | `{currentPassword, newPassword}` — fires *password changed* email |
| POST   | `/api/auth/forgot-password`       | `{email}` — always returns 200 (no enumeration). When the address resolves to an active user, sends a password-reset email with a 1-hour single-use token. |
| POST   | `/api/auth/reset-password`        | `{token, newPassword}` — consumes the reset token, sets the password, fires *password changed* email |
| POST   | `/api/auth/verify-email`          | `{token}` — flips `email_verified=true` on the token's user |
| POST   | `/api/auth/resend-verification`   | (auth required) re-issues a verify-email token; no-op if already verified |

### Admin endpoints (require `is_staff` or `is_superuser`)

| Method | Path                                       | Description                       |
| ------ | ------------------------------------------ | --------------------------------- |
| GET    | `/api/users`                               | list users                        |
| POST   | `/api/users`                               | create user (only superusers may set `isSuperuser`) |
| GET    | `/api/users/:id`                           | user with groups + permissions    |
| PATCH  | `/api/users/:id`                           | toggle `isActive`/`isStaff`/`isSuperuser` |
| DELETE | `/api/users/:id`                           | delete user (cannot delete self)  |
| POST   | `/api/users/:id/groups/:groupId`           | add user to group                 |
| DELETE | `/api/users/:id/groups/:groupId`           | remove user from group            |
| POST   | `/api/users/:id/password`                  | admin password reset (fires *password changed* email) |
| POST   | `/api/users/:id/resend-verification`       | admin re-issues a verify-email link for a user (no-op if already verified) |
| GET    | `/api/users/:id/permissions`               | direct (non-group) permissions    |
| PUT    | `/api/users/:id/permissions`               | `{codenames: [...]}` — full replace of direct perms |
| POST   | `/api/users/:id/permissions/:codename`     | grant a direct permission         |
| DELETE | `/api/users/:id/permissions/:codename`     | revoke a direct permission        |
| GET    | `/api/groups`                              | list groups                       |
| POST   | `/api/groups`                              | create group                      |
| DELETE | `/api/groups/:id`                          | delete group                      |
| GET    | `/api/groups/:id/permissions`              | permissions held by a group       |
| PUT    | `/api/groups/:id/permissions`              | `{codenames: [...]}` — full replace |
| GET    | `/api/permissions`                         | catalog (any logged-in user)      |

### How permission gating works

Every domain route is wrapped in `auth.RequirePerm("<codename>")`:

- CRUD: `view_<entity>` / `add_<entity>` / `change_<entity>` / `delete_<entity>` — derived from the resource's `Entity` field.
- Workflows: explicit codename per action (e.g. `approve_mileage_jmp`).
- Admin: `view_audit_entry`, `change_operator_ticker`, `export_data`, ...

Failure modes:

- No session cookie / expired session → `401 Unauthorized`
- Authenticated but missing the codename → `403 Forbidden` with `{error, need}`

### Front-end integration

Send `credentials: "include"` on every fetch so the cookie travels:

```js
await fetch("/api/auth/login", {
  method: "POST",
  credentials: "include",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ username, password }),
});
```

CORS responses include `Access-Control-Allow-Credentials: true` and the
configured `Access-Control-Allow-Origin`.

### Manual smoke test

After `go run ./cmd/seed --reset` against a clean database:

```sh
# Boot the server in one shell
DATABASE_URL=... go run .

# Log in (saves cookie to ./cookies.txt)
curl -i -c cookies.txt -X POST http://localhost:8080/api/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"admin"}'

# Whoami
curl -s -b cookies.txt http://localhost:8080/api/auth/me | jq .

# Try a permission-gated route as the admin (200 OK)
curl -s -b cookies.txt http://localhost:8080/api/vehicles

# Same call without the cookie (401)
curl -s -i http://localhost:8080/api/vehicles

# Create a viewer-only user, log in as them, expect 403 on writes
curl -s -b cookies.txt -X POST http://localhost:8080/api/users \
    -H 'Content-Type: application/json' \
    -d '{"username":"alex","password":"alexalex8","groupIds":[(SELECT id FROM auth_groups WHERE name='\''viewer'\'')]}'

# Log out
curl -s -b cookies.txt -X POST http://localhost:8080/api/auth/logout
```

### Email pipeline

Transactional email is sent via `internal/mail`. Two implementations:

- **`SMTPMailer`** — used when `SMTP_HOST` is set. Dials the host with `net/smtp`, sends `multipart/alternative` (text + HTML).
- **`LogMailer`** — fallback when `SMTP_HOST` is empty. Renders the email and writes it to stdout. Lets you copy the verify/reset link straight from the server log during dev.

Templates live in [internal/mail/templates/](./internal/mail/templates) and are embedded into the binary via `embed.FS`. Each message has both `<name>.html` and `<name>.txt`:

| Template            | Sent when                                                              |
| ------------------- | ---------------------------------------------------------------------- |
| `welcome`           | Admin creates a user with `emailVerified=true` (no verification needed) |
| `verify_email`      | Admin creates a user without pre-verification, or `/auth/resend-verification` |
| `password_reset`    | `/auth/forgot-password` finds the address                              |
| `password_changed`  | After every password change (self, admin reset, or post-reset confirmation) |

Email sending is fire-and-forget on a 30-second background context — slow SMTP doesn't block the request. Failures are logged.

### Token model

`auth_tokens` holds both reset-password and verify-email tokens, distinguished by `purpose`. The plaintext token is 32 random bytes (base64url) and only the SHA-256 hex digest is persisted. Tokens are single-use (a `used_at` stamp) and short-lived: reset = 1 hour, verify = 24 hours. Issuing a new token invalidates prior unused tokens for the same `(user, purpose)` so an old reset link can't be replayed if the user clicks "send again".

### Frontend link contract

Email links assume these frontend routes exist (build them in the Next.js app):

| Email                | Link shape                                                    |
| -------------------- | ------------------------------------------------------------- |
| `welcome`            | `${APP_URL}/login`                                            |
| `verify_email`       | `${APP_URL}/verify-email?token=<token>` → page calls `POST /api/auth/verify-email` |
| `password_reset`     | `${APP_URL}/reset-password?token=<token>` → page calls `POST /api/auth/reset-password` |
| `password_changed`   | `${APP_URL}/forgot-password` (footer recovery link)           |

## IoT / GPS live tracking, history, fuel monitoring

Ingest runs in **Fleet_IoT** (`edge/Fleet_IoT`); this service reads the same **`telemetry_timeseries`** Timescale hypertable via the shared `github.com/iag/fleet-iot/iot` module.

1. **HTTP bulk ingest** — `POST /api/iot/pings` or `POST /v1/pings` on Fleet_IoT (`:4080`, or via gateway `/api/v1/fleet/api/iot/pings`). Device API key in `Authorization: Bearer <key>`. Up to 1000 pings per request.
2. **Native Codec 8/8E TCP** — Fleet_IoT `cmd/gateway` on `:5027` (`IOT_ADDR`). Teltonika units identify by IMEI against `iot_devices.serial`.

Both paths call `iot.Store.InsertPings` (`pgx.CopyFrom` into `telemetry_timeseries`).

### Endpoints

| Method | Path                                       | Auth                     | Description                                       |
| ------ | ------------------------------------------ | ------------------------ | ------------------------------------------------- |
| GET    | `/api/iot/devices`                         | `manage_iot_device`      | list registered devices                           |
| POST   | `/api/iot/devices`                         | `manage_iot_device`      | `{ serial, label?, vehicleId?, issueKey? }` — when `issueKey=true` the response includes the plaintext `apiKey` **once** |
| GET    | `/api/iot/devices/:id`                     | `manage_iot_device`      | one device                                        |
| PATCH  | `/api/iot/devices/:id`                     | `manage_iot_device`      | `{ label?, vehicleId?, isActive? }`               |
| DELETE | `/api/iot/devices/:id`                     | `manage_iot_device`      | remove device                                     |
| POST   | `/api/iot/devices/:id/rotate-key`          | `manage_iot_device`      | issue a fresh API key (returns plaintext once)    |
| POST   | `/api/iot/pings` (Fleet_IoT, not fleet API) | device `Authorization`  | bulk-ingest pings. See `GET /api/iot/ingestion` on fleet for URLs. |
| GET    | `/api/vehicles/:id/track?from=&to=&limit=` | `view_telemetry`         | playback over a time window (range capped at **14 days**, max 50,000 rows). RFC3339 timestamps. |
| GET    | `/api/vehicles/:id/track/latest`           | `view_telemetry`         | most recent ping                                  |
| GET    | `/api/vehicles/:id/track/stream`           | `view_telemetry`         | **SSE** live stream (`event: ping` frames). 2-second poll fallback for cross-process pings (gateway). |
| GET    | `/api/vehicles/live/stream`                | `view_vehicle` *or* `view_telemetry` | **SSE** fleet snapshot (`event: fleet` every ~3s) — vehicle hot positions from DB, powers the Next.js map shell. |

### Schema

- `iot_devices(id, serial, label, vehicle_id, api_key_hash, is_active, last_seen, last_ip, created_at)` — physical units. `api_key_hash` is `sha256(plaintext)` — plaintext is shown once on creation/rotate.
- `telemetry_timeseries(...)` — raw position fixes (Timescale hypertable on `ts` when the extension is installed; see migration `0010_telemetry_timeseries.sql`). `raw` JSONB holds the full device IO map. Indexed by `(vehicle_id, ts DESC)` btree + BRIN on `ts`.
- `telemetry_daily(vehicle_id, day, ping_count, distance_km, max_speed_kmh, avg_speed_kmh, fuel_used_litres, moving_minutes, idle_minutes, first_ping, last_ping)` — survives raw retention (populated by a future `cmd/telemetry-aggregate`).

### Volume + retention decisions

Sized for **~50 vehicles × 1 ping/min ≈ 26M pings/year**. Defaults:

- TimescaleDB recommended (`timescale/timescaledb` in Compose); falls back to a plain heap table without the extension.
- 365-day raw retention; drop daily via `go run ./cmd/telemetry-purge --days 365`.
- Daily aggregates kept indefinitely; populated nightly by `go run ./cmd/telemetry-aggregate` (defaults to "yesterday UTC, every vehicle that had pings").

**Recommended cron** (just after midnight UTC):

```cron
05 0 * * *  cd /opt/haula-backend && DATABASE_URL=... go run ./cmd/fleet-jobs --all --purge-days 365
```

Equivalent split (same effect as `--all`):

```cron
05 0 * * *  cd /opt/haula-backend && DATABASE_URL=... go run ./cmd/telemetry-aggregate
30 0 * * *  cd /opt/haula-backend && DATABASE_URL=... go run ./cmd/telemetry-purge --days 365
```

The aggregator is idempotent (`ON CONFLICT DO UPDATE`), so re-running a day overwrites rather than duplicates.

### Fuel analytics

`telemetry_timeseries.fuel_level` carries the continuous tank reading from the device (the gateway pulls Teltonika IO ID `89` by default, scaled `× 0.1` to a percentage; HTTP relays send whatever the device reports). The vehicle's hot-state `fuel` column is synced from the freshest ping in each batch.

Tank size lives on `vehicles.tank_capacity_litres` (seed data ships sensible defaults for the demo fleet). With both pieces, the nightly aggregator does the rest:

- **`telemetry_daily.fuel_used_litres`** — sum of negative deltas between consecutive pings, **excluding** refuel jumps (so refilling the tank doesn't read as use).
- **`fuel_events`** — auto-detected refuels and anomalous drops:
  - `refuel` = positive delta ≥ 5%
  - `drop` = negative delta ≤ -3% within ≤ 5 minutes (longer gaps are likely just normal driving consumption)
  - **Confidence** is set heuristically: a drop while parked + ignition off is `high` (textbook theft pattern); a drop while moving fast is `low` (probably just consumption); a refuel while moving is `low` (probably a sensor glitch).

`fuel_events` is **separate** from `fuel_records` (manual / CSV refuel ledger) — we don't conflate detected events with human entries; matching the two is left for a follow-up.

Re-running the aggregator over the same day is idempotent: `telemetry_daily` upserts on `(vehicle_id, day)`, and `fuel_events` is `ON CONFLICT (vehicle_id, ts, kind) DO NOTHING`.

#### Fuel endpoints (perm: **`view_telemetry` _or_ `view_fuel_record`**)

Matches the HAULA frontend: operators with fuel-ledger access see IoT fuel charts and fleet anomalies without requiring full GPS replay permission.

| Method | Path                                           | Description                                                  |
| ------ | ---------------------------------------------- | ------------------------------------------------------------ |
| GET    | `/api/vehicles/:id/fuel/history?from=&to=`     | the fuel-level time series for one vehicle (range capped at **31 days**) |
| GET    | `/api/vehicles/:id/fuel/events?from=&to=&kind=&confidence=` | filter by `kind` (refuel/drop) and/or `confidence` (same **31-day** cap) |
| GET    | `/api/vehicles/:id/fuel/summary?from=&to=`     | distance, fuel used, **km/L efficiency**, refuel/drop counts and litres (same cap) |
| GET    | `/api/fuel/anomalies?from=&to=&limit=`         | fleet-wide hot-list (every drop + low-confidence refuel; same cap)     |

### Codec 8 IO IDs the gateway extracts by default

| IO ID | Field             | Notes                                           |
| ----- | ----------------- | ----------------------------------------------- |
| 199   | `odo` (km)        | Total odometer (m → km)                          |
| 89    | `fuel_level` (%)  | Stored as % × 10 by Teltonika; we divide by 10  |
| 239   | `ignition`        | 0/1                                             |

If your fleet's firmware exposes fuel on a different IO ID (e.g. analog `9`), update the constants at the top of [cmd/iot-gateway/main.go](./cmd/iot-gateway/main.go).

### Manual smoke test (HTTP path)

```sh
# Register a device and grab its plaintext API key
curl -b cookies -X POST http://localhost:8080/api/iot/devices \
    -H 'Content-Type: application/json' \
    -d '{"serial":"352094088112233","vehicleId":"V01","issueKey":true}'
# → { "id": 1, "apiKey": "abc...xyz", ... }

# Push a ping as the device
curl -X POST http://localhost:8080/api/iot/pings \
    -H 'Authorization: Bearer abc...xyz' \
    -H 'Content-Type: application/json' \
    -d '{"lat":-0.880,"lng":30.265,"speedKmh":42,"fuelLevel":62,"ignition":true}'

# Replay
curl -b cookies "http://localhost:8080/api/vehicles/V01/track?from=2026-05-03T00:00:00Z&to=2026-05-04T00:00:00Z" | jq

# Live stream (SSE)
curl -N -b cookies http://localhost:8080/api/vehicles/V01/track/stream
```

### Production hardening

The server now ships with the operational basics enabled by default:

- **Graceful shutdown** — `main.go` runs an `http.Server` in a goroutine, waits for `SIGINT` / `SIGTERM`, and calls `Shutdown(ctx)` with a 30-second drain window. The IoT TCP gateway has the same shape: closes its listener and waits up to 30s for in-flight connections to finish their current AVL packet.
- **Structured logging** — every API/gateway log line goes through `log/slog`. `LOG_FORMAT=json` for production (default `text` for dev). `LOG_LEVEL=debug` raises verbosity.
- **CSRF protection** — double-submit cookie. Login response sets a non-HttpOnly `csrf_token` cookie *and* echoes the value in the JSON body; the client must send it back as `X-CSRF-Token` on every state-changing cookie-authed request. Skipped for `Authorization: Bearer` paths (IoT ingestion) and the public auth endpoints (login itself, forgot-password, reset-password, verify-email).
- **Rate limits** — in-memory token-bucket limiters per route:

  | Route                              | Per-key limit                  |
  | ---------------------------------- | ------------------------------ |
  | `/api/auth/login`                  | 20/min per IP, burst 5         |
  | `/api/auth/forgot-password`        | 10/min per email (or per IP if email is unparseable) |
  | `/api/auth/resend-verification`    | 6/min per IP                   |
  | `/api/iot/pings`                   | 120/min per IP, burst 30       |
  | (everything else)                  | 300/min per IP                 |

  Single-instance only — for HA deployments, swap the in-memory limiter for a Redis-backed implementation; the public surface is just a `gin.HandlerFunc` factory in [`internal/security/ratelimit.go`](./internal/security/ratelimit.go).

### Frontend integration: the CSRF dance

```ts
// 1. Login. Capture the csrfToken from the JSON body.
const r = await fetch("/api/auth/login", {
  method: "POST",
  credentials: "include",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ username, password }),
}).then(r => r.json());
const csrf = r.csrfToken;

// 2. Every subsequent state-changing request includes the header.
await fetch("/api/vehicles", {
  method: "POST",
  credentials: "include",
  headers: {
    "Content-Type": "application/json",
    "X-CSRF-Token": csrf,
  },
  body: JSON.stringify(newVehicle),
});
```

### Container image

Build a slim distroless image with all four binaries baked in:

```sh
docker build -t haula-fleet ./backend

# Default entrypoint is the API server.
docker run --rm -p 8080:8080 -e DATABASE_URL=... haula-fleet

# Run the other commands by overriding the entrypoint.
docker run --rm -e DATABASE_URL=... --entrypoint /app/seed              haula-fleet --reset
docker run --rm -e DATABASE_URL=... --entrypoint /app/iot-gateway       haula-fleet
docker run --rm -e DATABASE_URL=... --entrypoint /app/createsuperuser   haula-fleet --username alex --password 's3cret!'
docker run --rm -e DATABASE_URL=... --entrypoint /app/telemetry-purge   haula-fleet --days 365
docker run --rm -e DATABASE_URL=... --entrypoint /app/telemetry-aggregate haula-fleet
```

The image is `gcr.io/distroless/static-debian12:nonroot` — no shell, no package manager, runs as a non-root uid by default. Ship behind your normal HTTPS terminator (nginx / Caddy / ALB / CloudFront); set `COOKIE_SECURE=true` in production so session cookies require HTTPS.

### Security caveats / future work

- **CSRF**: now enforced — see "Production hardening" above. Re-evaluate skip list before exposing publicly.
- **Email-driven flow rate limits**: forgot-password and resend-verification are not throttled. A bad actor can hammer them. Add a per-user/per-IP cooldown before going public.
- **Object-level / row-level permissions**: only model-level today. A driver currently sees *all* JMPs they have `view_jmp` on, not just their own. Implementing scope filters is a separate task.
- **Password policy / lockout / 2FA**: not implemented.
- **Audit user**: pulled from the active session (`auth.UserFrom(c).Username`); falls back to the `X-Operator` header (legacy) and finally to the ticker operator.
- **Audit log persistence**: now in Postgres (`audit_entries`); writes resolve `user_id` from the username when the user exists, otherwise just record the username string snapshot.
- **Telemetry → vehicles hot-state sync**: the gateway and HTTP ingestion call `SyncVehicleFromPing`, which updates the same `vehicles` row the CRUD endpoints serve, so the freshest position appears on `GET /api/vehicles/:id` automatically.
- **Cross-process SSE**: the in-process broker only fans out pings ingested via the same API instance. Pings from `cmd/iot-gateway` (separate process) reach SSE clients via the 2-second poll fallback. For sub-second cross-process live, swap the broker for Postgres `LISTEN/NOTIFY` or Redis pubsub.
- **Codec 8E variable-length IO**: the parser advances the cursor past each variable-length element but doesn't currently persist the payload. Add to `raw` JSON if you need CAN frames on the wire.

The `X-Operator` request header overrides the audit user for a single call;
without it, audit entries fall back to the stored ticker operator.

## Notes

- IDs are server-generated as `<PREFIX>-<8-hex>` when a `POST` body omits `id`.
- `Collection.Add` prepends, mirroring `[item, ...prev]` in the frontend store.
- JSON field names match the TS interfaces exactly so the frontend can
  drop-in-replace localStorage with `fetch` calls.
