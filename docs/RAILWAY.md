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

## Commits not triggering builds?

1. **Settings → Source** — confirm repo/branch match `iag-fleet` / `main`.
2. **Settings → Build** — builder should be **Dockerfile** (or use `railway.toml` in this repo).
3. **Settings → Deploy** — enable **Deploy on push** / watch branch `main`.
4. **Deployments** → **Redeploy** → pick commit `7f7d0f3` or latest `main` manually.
5. Reconnect GitHub: disconnect and re-link the repo if webhooks are stale.

## Required environment variables

See `config/.env.production.example`. Minimum:

- `DATABASE_URL` (Postgres plugin or external)
- `REDIS_URL` (optional but recommended for seed cache + notifications)
- `JWT_SECRET` (≥32 chars) if using local JWT mode, or gateway vars if behind platform auth
- `GATEWAY_INTERNAL_SECRET`, `AUTH_MODE=gateway`, `JWKS_URL`, `JWT_ISSUER` for production gateway auth

## Health check

Railway should probe `GET /ready` (configured in `railway.toml`).
