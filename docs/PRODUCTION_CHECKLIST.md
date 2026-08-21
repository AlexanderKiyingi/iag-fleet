# Fleet — production checklist

Use this before enabling Fleet in staging/production.

## Required

| Item | Env / setting | Verify |
|------|----------------|--------|
| Operational DB | `DATABASE_URL` → `iag_platform` (migrations through `0017_event_outbox`) | `GET /ready` returns ok |
| Telemetry DB | `TELEMETRY_DATABASE_URL` → dedicated Timescale (`iag_telemetry`) when split | `iag_fleet.telemetry_timeseries` exists (see `deploy/postgres/telemetry-init/03-telemetry-tables.sql` on fresh volumes) |
| Fleet_IoT registry | `REGISTRY_DATABASE_URL` on ingest/gateway → same DSN as Fleet `DATABASE_URL` | Live map updates after device ping |
| Auth | `JWT_ISSUER`, `JWKS_URL`, `AUDIENCE=iag.fleet` | Mutating API returns 401 without Bearer |
| Service account | `SERVICE_CLIENT_SECRET` (≥16 chars) | Startup log: permissions registered |
| Environment | `ENVIRONMENT=production` | `reset_data` / `simulate_vehicles` return 403 |
| Migrations | `AUTO_MIGRATE=false` | Run `/app/migrate` before starting the API; `/app/migrate -dry-run` reports 0 pending afterwards |
| Kafka publish | `EVENT_BUS_ENABLED=true`, `KAFKA_BROKERS` | Vehicle create emits `fleet.vehicle.created` via outbox |
| Redis | `REDIS_URL` when Fleet API and Fleet_IoT are separate processes | SSE `/api/fleet-live/stream` receives pings |

## Recommended

| Item | Notes |
|------|--------|
| `PUBLIC_API_URL` | Gateway origin for docs and callbacks |
| `ALLOWED_ORIGINS` | Explicit CORS list (not `*`) |
| `TELEMETRY_INGEST_URL` | Gateway upstream to Fleet_IoT HTTP ingest |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Trace export to collector |
| `NOTIFICATIONS_SCAN_SEC` | In-app bell scan interval (default 60s); also recomputes compliance status |
| PM cron | `fleet-jobs --evaluate-pm --mark-mx-overdue` daily (or `POST /api/pm-schedules/evaluate` with service token) |
| Compliance cron | `fleet-jobs --recompute-compliance` daily (redundant with notification scan but useful for batch audit) |

## Kubernetes

Manifests: [`deploy/kubernetes/fleet/`](../../../../deploy/kubernetes/fleet/)

1. Copy `secret.example.yaml` → sealed secret / external secrets operator.
2. Apply configmap + deployment + service.
3. Point gateway `UPSTREAM_FLEET` at `iag-fleet:4008` and `UPSTREAM_FLEET_IOT_INGEST` at ingest service.

## Integration tests (optional CI job)

```bash
TEST_DATABASE_URL=postgres://svc_iag_fleet:PASSWORD@HOST:5432/iag_platform?sslmode=disable \
  go test ./internal/handlers/... -run Integration -v
```

## Smoke test (post-deploy)

```bash
curl -s https://api.example.com/api/v1/fleet/ready

curl -s -H "Authorization: Bearer $TOKEN" \
  https://api.example.com/api/v1/fleet/api/vehicles?limit=5

curl -s -H "Authorization: Bearer $TOKEN" \
  https://api.example.com/api/v1/fleet/api/vehicles/VEH-XXXX/track/latest
```

## Domain events (iag.fleet)

- **Vehicle created/updated/deleted** → outbox → `fleet.vehicle.*`
- **Status change** (registry patch or telemetry sync) → `fleet.vehicle.status_changed`
- **PM due** (notification scan) → `fleet.pm.due`
- **Maintenance created** (PM evaluate) → `fleet.maintenance.created`
- **Maintenance completed** → `fleet.maintenance.completed`
- **Compliance expiring** (notification scan) → `fleet.compliance.expiring`
- **Compliance renewed** → `fleet.compliance.renewed`
- **Fuel / JMP / cargo** → existing workflow events (best-effort or outbox where wired)
