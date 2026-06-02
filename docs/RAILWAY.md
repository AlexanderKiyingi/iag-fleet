# Deploying fleet on Railway

## GitHub repo

Use the **standalone** service repo (not the monorepo path alone):

| Setting | Value |
|---------|--------|
| Repository | `AlexanderKiyingi/iag-fleet` |
| Branch | `main` |
| Root directory | `/` (repo root — `Dockerfile` is here) |

If the service is wired to `IAG_multi_backend` instead, set **Root directory** to `services/operations/fleet`, **Dockerfile path** `Dockerfile`, and build target **`monorepo`** (Compose uses the same target). Prefer the standalone `iag-fleet` repo for simpler Railway builds.

### `github.com/iag/fleet-iot` dependency

The standalone Dockerfile **clones** [Fleet_IoT](https://github.com/AlexanderKiyingi/Fleet_IoT) at build time (`FLEET_IOT_REF`, default `main`). No monorepo `edge/` path is required on Railway.

Optional build arg: `FLEET_IOT_REF` (git branch/tag) to pin the ingest module version.

## Postgres (fix “connection refused” on 127.0.0.1:5432)

That error means `DATABASE_URL` still points at **localhost** (usually copied from `.env.example`):

```text
postgres://svc_iag_fleet:iag_fleet_dev@localhost:5432/iag_platform?sslmode=disable
```

Inside Railway there is no Postgres on `127.0.0.1` — you need the **Railway Postgres** hostname.

### Steps

1. In your Railway **project**, click **+ New** → **Database** → **PostgreSQL** (if you do not already have one).
2. Open the **fleet** service → **Variables**.
3. **Remove** any hand-typed `DATABASE_URL` that contains `localhost` or `127.0.0.1`.
4. Add a variable reference from the Postgres service:
   - Click **+ New Variable** → **Add Reference** → choose your Postgres service → **`DATABASE_URL`**.
   - Railway sets something like  
     `postgresql://postgres:…@monorail.proxy.rlwy.net:12345/railway`
5. On a shared Postgres instance, use database `iag_platform` (or Railway’s default `railway`) and role `svc_iag_fleet`:
   ```text
   postgresql://svc_iag_fleet:PASSWORD@HOST:PORT/railway?sslmode=require
   ```
   Bootstrap once: `deploy/postgres/init/01-schemas.sql` + `02-service-roles.sh` (role owns `iag_fleet` schema).
6. Leave `AUTO_MIGRATE=true` (default) so pending migrations apply on each deploy (includes `telemetry_timeseries` + Timescale — see `deploy/postgres/TIMESCALE.md`).
7. Redeploy the fleet service.

For Postgres created before Timescale, run migration `0012_timescale_existing_volume` via `AUTO_MIGRATE` or follow `deploy/postgres/TIMESCALE.md`. On managed Postgres without the Timescale add-on (e.g. Railway's default), the migration is a no-op — `telemetry_timeseries` stays a regular heap table (same fallback as `0010`).

The API also needs non-local URLs for auth/notifications when integrated; see `config/.env.production.example`.

## Port and health checks

Railway routes traffic and runs health checks against the **`PORT`** variable, not `ADDR`.

| Variable | Notes |
|----------|--------|
| `PORT` | Set to **`4008`** in the fleet service variables (or use the Dockerfile default). The API and `/ready` probe listen on this port. |
| `ADDR` | Optional override for local/docker-compose (`:4008`). Ignored when `PORT` is set. |

Do **not** leave `ADDR=:4008` in Railway while `PORT` points at another port — health checks will fail with “service unavailable”.

## Other required variables

| Variable | Notes |
|----------|--------|
| `DATABASE_URL` | From Postgres plugin (see above) — **not** localhost |
| `AUTO_MIGRATE` | `true` (default; applies pending migrations each deploy) |
| `JWKS_URL`, `JWT_ISSUER` | Your auth service — fleet verifies Bearer JWTs locally against this |
| `CORS_ORIGIN`, `PUBLIC_API_URL` | Your frontend / gateway URLs |

> **Auth (post hard-cutover):** fleet runs pure **Bearer+aud** — every
> request must carry a JWT with `aud=iag.fleet`, verified locally via JWKS.
> `AUTH_MODE` and `GATEWAY_INTERNAL_SECRET` are **no longer read by the
> code** (the Dockerfile still sets `AUTH_MODE=gateway`, but it is a dead
> no-op). Don't bother setting either on Railway; just point `JWKS_URL` /
> `JWT_ISSUER` at the authentication service.

`REDIS_URL` and Kafka are optional; the API starts without them.

## Commits not triggering builds?

1. **Settings → Source** — repo `iag-fleet`, branch `main`, root `/`.
2. **Settings → Deploy** — enable deploy on push.
3. **Deployments → Redeploy** — pick latest `main`.
4. Reconnect GitHub if webhooks are stale after a force-push.

## Health check

Railway probes `GET /ready` (`railway.toml`). Probes are **unauthenticated** by design (the health/readiness paths bypass the Bearer+aud middleware). A `401` on `/ready` means an old, pre-cutover build is running; redeploy `main`.
