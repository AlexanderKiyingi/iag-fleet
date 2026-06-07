# IAG Fleet — platform integration

Fleet is a domain microservice behind the **API gateway**, using **iag-authentication** for IAM, **iag-notifications** for outbound email/SMS, and **iag-accounts** for fuel expense ledger entries.

**Telemetry ingest** runs in **Fleet_IoT** (`edge/Fleet_IoT`): HTTP `:4080/v1/pings` and Teltonika TCP `:5027`, writing pings to the dedicated **`telemetry_timeseries`** Timescale hypertable. Fleet keeps the **operational registry** (`vehicles`, `iot_devices`) on `DATABASE_URL`; ingest syncs vehicle hot-state to that same DB via `REGISTRY_DATABASE_URL`.

In split-DB production, Fleet API reads telemetry from `TELEMETRY_DATABASE_URL` and serves CRUD from `DATABASE_URL`. See [PRODUCTION_CHECKLIST.md](./PRODUCTION_CHECKLIST.md).

## Services

| Service | Integration | Mechanism |
|---------|-------------|-----------|
| **iag-authentication** | Users, groups, `fleet.*` permissions | Bearer JWT verified locally via authentication JWKS (`aud=iag.fleet`) |
| **iag-api-gateway** | Public ingress | Clients call `PUBLIC_API_URL/api/v1/fleet/api/...` |
| **iag-notifications** | Critical email alerts | Kafka `iag.notifications` → `notification.requested` (`fleet.alert` template) |
| **iag-finance** | Fuel purchase ledger | Kafka `iag.finance` → `fleet.fuel.recorded` on fuel record create/update |

## Auth (post hard-cutover)

Fleet runs a **single** auth path: every request (except the public health
probes) must carry a Bearer JWT with `aud=iag.fleet`, verified locally
against the authentication service's JWKS — no callback on the hot path.
The old `gateway` header-trust mode (`X-IAG-*` + `GATEWAY_INTERNAL_SECRET`)
was **removed** during the platform hard cutover; the code no longer reads
`AUTH_MODE` or `GATEWAY_INTERNAL_SECRET`. See
[FRONTEND_INTEGRATION.md §1](./FRONTEND_INTEGRATION.md) for the token flow.

## Environment

| Variable | Purpose |
|----------|---------|
| `DATABASE_URL` | Operational Postgres (`iag_platform`) — vehicles, drivers, outbox |
| `TELEMETRY_DATABASE_URL` | Timescale read pool (`iag_telemetry`); omit when single-DB |
| `ENVIRONMENT` | `production` disables `reset_data` / `simulate_vehicles` |
| `JWKS_URL` | Authentication JWKS endpoint — fleet verifies Bearer JWTs against this |
| `JWT_ISSUER` | Expected token issuer (authentication service) |
| `PUBLIC_API_URL` | Gateway origin, e.g. `http://localhost:8080` |
| `GATEWAY_API_PREFIX` | `/api/v1/fleet` |
| `AUTHENTICATION_URL` | Health probe + docs, e.g. `http://authentication:3001` |
| `NOTIFICATIONS_URL` | Health probe, e.g. `http://notifications:3002` |
| `FINANCE_URL` | Optional health probe, e.g. `http://finance:3006` |
| `EVENT_BUS_ENABLED` | `true` to publish Kafka events |
| `KAFKA_BROKERS` | e.g. `redpanda:9092` |
| `FLEET_FUEL_CURRENCY` | Currency for `fleet.fuel.recorded` (default `UGX`) |

## Local development (full stack)

```bash
# From repo root — starts postgres, auth, notifications, accounts, fleet, api-gateway, redpanda
pnpm infra:up

# Fleet API via gateway
curl http://localhost:8080/api/v1/fleet/health

# Staff: integration status (requires platform login + staff)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/fleet/api/platform/status
```

## Gateway routes

| Public path | Upstream |
|-------------|----------|
| `/api/v1/fleet/api/*` | Fleet `:4008` |
| `/api/v1/authentication/*` | Authentication `:3001` |
| `/api/v1/notifications/*` | Notifications `:3002` |
| `/api/v1/accounts/*` | Accounts `:3005` |

## Events

Outbound registry mutations use a transactional **outbox** (`fleet_event_outbox`) when `EVENT_BUS_ENABLED=true` and Postgres is enabled — same pattern as CRM/DMS. Fleet_IoT ingest and `fleet-jobs --mark-stale` also enqueue `fleet.vehicle.status_changed` on the operational DB when telemetry updates registry status.

| Topic | Event types |
|-------|-------------|
| `iag.fleet` | `fleet.vehicle.created`, `fleet.vehicle.status_changed`, `fleet.jmp.completed`, `fleet.telemetry.refuel_detected`, … |
| `iag.notifications` | `notification.requested` |
| `iag.finance` | `fleet.fuel.recorded` (finance consumer books journal entries) |

## In-app notifications

- `notifications.user_id` is the authentication UUID (TEXT).
- Register via `GET /api/v1/fleet/api/users/me` after login.
- Bell fan-out uses `notification_recipients` (local DB).

## Permissions

Groups: `fleet-admin`, `fleet-manager`, `fleet-dispatcher`, `fleet-viewer` (seeded in authentication).

Codenames use `fleet.*` prefix; API accepts unprefixed aliases (`view_vehicle` ↔ `fleet.view_vehicle`).
