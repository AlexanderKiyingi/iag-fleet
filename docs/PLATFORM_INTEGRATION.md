# IAG Fleet — platform integration

Fleet is a domain microservice behind the **API gateway**, using **iag-authentication** for IAM, **iag-notifications** for outbound email/SMS, and **iag-accounts** for fuel expense ledger entries.

## Services

| Service | Integration | Mechanism |
|---------|-------------|-----------|
| **iag-authentication** | Users, groups, `fleet.*` permissions | Gateway JWT → `X-IAG-*` headers; optional direct JWKS in `AUTH_MODE=jwt` |
| **iag-api-gateway** | Public ingress | Clients call `PUBLIC_API_URL/api/v1/fleet/api/...` |
| **iag-notifications** | Critical email alerts | Kafka `iag.notifications` → `notification.requested` (`fleet.alert` template) |
| **iag-accounts** | Fuel purchase ledger | Kafka `iag.finance` → `fleet.fuel.recorded` on fuel record create/update |

## Auth modes

| Mode | Use |
|------|-----|
| `gateway` | Production / Compose — trust `X-IAG-*` + `GATEWAY_INTERNAL_SECRET` |
| `jwt` | Local dev without gateway — verify RS256 via authentication JWKS |

## Environment

| Variable | Purpose |
|----------|---------|
| `AUTH_MODE` | `gateway` (default) or `jwt` |
| `GATEWAY_INTERNAL_SECRET` | Shared with api-gateway (min 16 chars) |
| `PUBLIC_API_URL` | Gateway origin, e.g. `http://localhost:8080` |
| `GATEWAY_API_PREFIX` | `/api/v1/fleet` |
| `AUTHENTICATION_URL` | Health probe + docs, e.g. `http://authentication:3001` |
| `NOTIFICATIONS_URL` | Health probe, e.g. `http://notifications:3002` |
| `ACCOUNTS_URL` | Health probe, e.g. `http://accounts:3005` |
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

| Topic | Event types |
|-------|-------------|
| `iag.fleet` | `fleet.jmp.completed`, `fleet.telemetry.refuel_detected`, … |
| `iag.notifications` | `notification.requested` |
| `iag.finance` | `fleet.fuel.recorded` (accounts consumer books journal entries) |

## In-app notifications

- `notifications.user_id` is the authentication UUID (TEXT).
- Register via `GET /api/v1/fleet/api/users/me` after login.
- Bell fan-out uses `notification_recipients` (local DB).

## Permissions

Groups: `fleet-admin`, `fleet-manager`, `fleet-dispatcher`, `fleet-viewer` (seeded in authentication).

Codenames use `fleet.*` prefix; API accepts unprefixed aliases (`view_vehicle` ↔ `fleet.view_vehicle`).
