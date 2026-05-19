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
postgres://iag:iag_dev@localhost:5432/iag_fleet?sslmode=disable
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
5. Optional: use database name `iag_fleet` instead of `railway`:
   - Connect to Postgres (Railway **Data** tab or `psql` with the plugin URL) and run:
     ```sql
     CREATE DATABASE iag_fleet;
     ```
   - Edit the fleet `DATABASE_URL` so the path is `/iag_fleet` (same user/password/host/port as the plugin URL).
   - Or keep the default `railway` database — migrations work on whatever name is in the URL.
6. Set `AUTO_MIGRATE=true` on first deploy so schema is created.
7. Redeploy the fleet service.

The API also needs non-local URLs for auth/notifications when integrated; see `config/.env.production.example`.

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
