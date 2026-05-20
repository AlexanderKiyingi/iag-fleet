# Deploying fleet on Railway

## GitHub repo

Use the **standalone** service repo (not the monorepo path alone):

| Setting | Value |
|---------|--------|
| Repository | `AlexanderKiyingi/iag-fleet` |
| Branch | `main` |
| Root directory | `/` (repo root — `Dockerfile` is here) |

If the service is wired to `IAG_multi_backend` instead, set **Root directory** to:

`services/operations/fleet`

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
6. Set `AUTO_MIGRATE=true` on first deploy so schema is created.
7. Redeploy the fleet service.

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
| `AUTO_MIGRATE` | `true` on first deploy |
| `GATEWAY_INTERNAL_SECRET` | ≥16 chars |
| `JWKS_URL`, `JWT_ISSUER` | Your auth service |
| `AUTH_MODE` | `gateway` (default in Dockerfile) |
| `CORS_ORIGIN`, `PUBLIC_API_URL` | Your frontend / gateway URLs |

`REDIS_URL` and Kafka are optional; the API starts without them.

## Commits not triggering builds?

1. **Settings → Source** — repo `iag-fleet`, branch `main`, root `/`.
2. **Settings → Deploy** — enable deploy on push.
3. **Deployments → Redeploy** — pick latest `main`.
4. Reconnect GitHub if webhooks are stale after a force-push.

## Health check

Railway probes `GET /ready` (`railway.toml`).
