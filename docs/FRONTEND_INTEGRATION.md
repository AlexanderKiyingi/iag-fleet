# Fleet Frontend Integration Guide

Comprehensive guide for connecting a frontend (Next.js, SvelteKit, plain SPA)
to the Fleet backend. Covers auth, the full HTTP route catalog, SSE streams,
permissions, and the shared TypeScript client.

For the short contract (base URL conventions only), see
[FRONTEND_CONTRACT.md](./FRONTEND_CONTRACT.md). This guide is the long form.

---

## 1. Authentication

Fleet runs in **platform Bearer+aud mode**. Every request — except the three
public probes (`/health`, `/healthz`, `/ready`) — requires:

```
Authorization: Bearer <jwt>
```

The JWT must carry `aud=iag.fleet` (the audience array on a user-principal
token already includes this; service-principal tokens issued for `iag.fleet`
also work). Fleet verifies the signature locally against the authentication
service's JWKS — it does not call auth on the request hot path.

The old gateway-forwarded-header trust path (`X-IAG-User-*`,
`GATEWAY_INTERNAL_SECRET`) was removed during the platform hard cutover. There
is no fallback.

### Token acquisition flow

```
┌─────────┐  1. POST /api/v1/authentication/oauth/token  ┌──────────┐
│ Browser │ ────────── grant_type=password ─────────────▶│   Auth   │
│  (FE)   │                                              │ Service  │
│         │◀────── access_token, refresh_token ──────────│          │
└─────────┘                                              └──────────┘
     │
     │  2. Authorization: Bearer <access_token>
     ▼
┌──────────┐
│  Fleet   │  (verifies JWT locally via cached JWKS)
└──────────┘
```

**Frontend responsibilities:**
- Store `access_token` in memory (not localStorage — XSS exposure).
- Store `refresh_token` in an httpOnly cookie if you control a BFF, otherwise
  in memory and accept that a tab reload forces re-login.
- Refresh before expiry: tokens default to **15-minute access TTL**, so kick
  off `POST /oauth/token` with `grant_type=refresh_token` ~1 minute before.
- On any `401` from fleet, attempt refresh; on `401` from refresh itself,
  redirect to login.

### Audience check failures

If the verifier rejects a token, fleet returns:

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json

{"error":"invalid token"}
```

Most common causes:
1. Token expired (refresh).
2. Token's `aud` claim doesn't include `iag.fleet` — issued for a different
   service. Re-login through auth, which embeds every backend audience.
3. JWKS rotation in flight — auth signs with a new kid the verifier hasn't
   fetched yet. Fleet refreshes JWKS every 5 minutes; transient.

---

## 2. Base URLs

| Environment | API base | SSE base |
|---|---|---|
| Local direct | `http://localhost:8082/api` | same |
| Local via gateway | `http://localhost:8080/api/v1/fleet/api` | same |
| Production | `https://<gateway>/api/v1/fleet/api` | same |

**Always go through the gateway in non-local environments** — it owns rate
limiting, CORS, request IDs, and routes `/api/v1/fleet/*` to this service.

### Required frontend env vars

```env
# Public — bundled into the client
NEXT_PUBLIC_FLEET_API_URL=http://localhost:8080/api/v1/fleet/api
NEXT_PUBLIC_AUTH_API_URL=http://localhost:8080/api/v1/authentication
NEXT_PUBLIC_GATEWAY_ORIGIN=http://localhost:8080

# Optional — when using a BFF for SSE proxying
FLEET_API_INTERNAL_URL=http://fleet:8082/api
```

### CORS

Fleet sets `Access-Control-Allow-Credentials: true` and allows the origins
listed in `CORS_ORIGIN` (default `http://localhost:3000,http://localhost:5173`).
**Auth is via the Authorization header — no cookies are required by fleet
itself.** The Credentials flag exists for legacy compatibility.

---

## 3. The TypeScript Client (`@iag/fleet-client`)

A minimal hand-written client lives at
[`packages/fleet-client`](../../../../packages/fleet-client). Today it covers
`me()`, `listVehicles()`, and `markNotificationSeen()` — a starting point, not
the whole surface. Extend it or call fetch directly for everything else.

```ts
import { FleetClient } from "@iag/fleet-client";

const fleet = new FleetClient({
  baseUrl: process.env.NEXT_PUBLIC_FLEET_API_URL!,
  getAccessToken: () => store.getState().auth.accessToken,
});

const me = await fleet.me();
```

When the surface grows large enough, switch to oapi-codegen against an
OpenAPI spec (not shipped today — see §10).

---

## 4. Complete Endpoint Catalog

All routes are prefixed with the base URL above. Permission codenames are
checked locally against the JWT's `permissions` claim — there's no callback
to auth.

### 4.1 Public (no auth)

| Method | Path | Description |
|---|---|---|
| GET | `/healthz` | Liveness — returns `{status:"ok"}` |
| GET | `/health` | Alias of `/healthz` |
| GET | `/ready` | Readiness (checks DB ping) |

### 4.2 Profile & platform

| Method | Path | Permission | Description |
|---|---|---|---|
| GET | `/api/users/me` | authenticated | Caller profile: id, email, isStaff, isSuperuser, groups, permissions |
| GET | `/api/platform/status` | staff+ | Health of auth, notifications, accounts, gateway |
| GET | `/api/reference` | authenticated | Enum reference data (departments, statuses, …); cached 10 min |
| GET | `/api/reference/geo` | authenticated | POIs, corridors, basemaps; cached 10 min |
| GET | `/api/ticker` | `view_operator_ticker` | Operator banner data (diesel price, UGX rate) |
| PATCH | `/api/ticker` | `change_operator_ticker` | Update banner |

### 4.3 Dashboard & analytics

| Method | Path | Permission | Cache | Description |
|---|---|---|---|---|
| GET | `/api/dashboard/summary` | authenticated | 30 s | KPIs + active alerts |
| GET | `/api/analytics/summary` | authenticated | 45 s | Driver scores, fuel trends, compliance state |
| GET | `/api/reports/summary` | authenticated | — | Period-scoped aggregated report |
| GET | `/api/calendar/events` | authenticated | — | Cross-module schedule (`from`/`to`, optional filters: `jmp`, `svc`, `cmp`, `req`, `cargo`, `fuel`) |

### 4.4 Standard CRUD resources

Every resource below exposes the same eleven operations. Substitute
`<resource>` for the path, `<entity>` for the permission codename.

| Method | Path | Permission |
|---|---|---|
| GET | `/<resource>` | `view_<entity>` |
| GET | `/<resource>/search` | `view_<entity>` |
| GET | `/<resource>/:id` | `view_<entity>` |
| POST | `/<resource>` | `add_<entity>` |
| POST | `/<resource>/bulk` | `add_<entity>` |
| PUT | `/<resource>/:id` | `change_<entity>` |
| PATCH | `/<resource>/:id` | `change_<entity>` |
| PATCH | `/<resource>/bulk` | `change_<entity>` |
| DELETE | `/<resource>/:id` | `delete_<entity>` |
| DELETE | `/<resource>/bulk` | `delete_<entity>` |

| Resource path | Entity (permission stem) | ID prefix |
|---|---|---|
| `/api/vehicles` | `vehicle` | `VEH` |
| `/api/drivers` | `driver` | `DRV` |
| `/api/jmps` | `jmp` | `JMP` |
| `/api/cargo` | `cargo` | `CRG` |
| `/api/cargo-docs` | `cargo_doc` | `DOC` |
| `/api/fuel` | `fuel_record` | `FUEL` |
| `/api/maintenance` | `maintenance_item` | `MX` |
| `/api/parts` | `part` | `PRT` |
| `/api/tyres` | `tyre` | `TYR` |
| `/api/trips` | `trip` | `TRP` |
| `/api/safety` | `safety_event` | `SAF` |
| `/api/compliance` | `compliance_item` | `CMP` |
| `/api/requests` | `service_request` | `REQ` |
| `/api/tasks` | `task_item` | `TSK` |
| `/api/deployment` | `deployment_day` | `DPL` |
| `/api/inspection-templates` | `inspection_template` | `TPL` |
| `/api/inspections` | `vehicle_inspection` | `INS` |
| `/api/pm-schedules` | `pm_schedule` | `PM` |

### 4.5 Pagination & filtering (`/search` endpoints)

```
GET /api/vehicles/search?limit=50&offset=0&orderBy=plate%20ASC&status=active

Response:
{
  "items":  [ ... ],
  "total":  413,
  "limit":  50,
  "offset": 0
}
```

- `limit` defaults to 50, capped at 500.
- `orderBy` accepts `<col> ASC|DESC`.
- Any other query param is treated as an equality filter on the matching
  column.

### 4.6 Workflow transitions

Routes that mutate multiple fields atomically.

| Method | Path | Permission | Body |
|---|---|---|---|
| POST | `/api/jmps/:id/complete-toolbox` | `complete_toolbox_jmp` | — |
| POST | `/api/jmps/:id/cancel` | `cancel_jmp` | — |
| POST | `/api/jmps/:id/approve-mileage` | `approve_mileage_jmp` | `{approved, notes?}` |
| POST | `/api/cargo/:id/set-stage` | `advance_stage_cargo` | `{stage, note?}` |
| POST | `/api/cargo/:id/advance-stage` | `advance_stage_cargo` | — |
| POST | `/api/cargo/:id/offload` | `offload_cargo` | — |
| POST | `/api/cargo/:id/demobilise` | `demobilise_cargo` | — |
| POST | `/api/cargo/:id/complete` | `advance_stage_cargo` | — |
| POST | `/api/requests/:id/assign` | `assign_request` | `{vehicleId, driverId, reviewerNotes?}` |
| POST | `/api/requests/:id/advance` | `change_service_request` | — |
| POST | `/api/requests/:id/create-jmp` | `add_jmp` | JMP fields |
| POST | `/api/tasks/:id/complete` | `complete_task` | — |
| POST | `/api/deployment/seed-today` | `seed_deployment` | — |
| POST | `/api/deployment/:id/entries` | `add_deployment_entry` | `{vehicleId, ...}` |
| POST | `/api/vehicles/simulate-tick` | `simulate_vehicles` | — |
| POST | `/api/parts/:id/movements` | `change_part` | `{qty, kind, ref?}` |
| POST | `/api/compliance/:id/renew` | `change_compliance_item` | renewal fields |
| POST | `/api/maintenance/:id/complete` | `change_maintenance_item` | — |
| POST | `/api/maintenance/:id/advance-status` | `change_maintenance_item` | — |
| POST | `/api/safety/:id/advance-status` | `change_safety_event` | — |
| POST | `/api/inspections/:id/submit` | `change_vehicle_inspection` | checklist body |
| POST | `/api/inspections/:id/create-defect-wo` | `change_vehicle_inspection` | — |

### 4.7 Fuel (custom analysis)

| Method | Path | Permission | Description |
|---|---|---|---|
| POST | `/api/fuel/:id/anomaly-event` | `change_fuel_record` | Mark anomaly on a fuel record |
| POST | `/api/fuel/link-events` | `change_fuel_record` | Auto-link manual fuel records to IoT events (query: `lookbackDays`, default 90) |
| GET | `/api/fuel/anomalies` | `view_telemetry` OR `view_fuel_record` | Fleet-wide anomaly hot list (`from`, `to`, `limit`, range ≤31 d) |

### 4.8 PM schedules (custom)

| Method | Path | Permission | Description |
|---|---|---|---|
| GET | `/api/pm-schedules/due` | `view_pm_schedule` | Due items (`withinDays=14`, `withinKm=500`) |
| POST | `/api/pm-schedules/evaluate` | `change_pm_schedule` | Evaluate schedules against current fleet state |

### 4.9 Routing (OSRM)

| Method | Path | Permission | Description |
|---|---|---|---|
| GET | `/api/routing/capabilities` | `view_vehicle` OR `view_telemetry` | OSRM profiles + maxWaypoints |
| POST | `/api/routing/plan` | `view_vehicle` OR `view_telemetry` | `{profile, points:[{lat,lng},…]}` (2–25 points) → `{distanceM, durationS, coordinates, mode}` |

### 4.10 Telemetry (read-only; see §6 for the live stream)

| Method | Path | Permission | Description |
|---|---|---|---|
| GET | `/api/vehicles/:id/track` | `view_telemetry` | GPS playback (`from`, `to` RFC3339, `limit`; range ≤14 d, ≤50 k rows) |
| GET | `/api/vehicles/:id/track/latest` | `view_telemetry` | Most recent ping |
| GET | `/api/vehicles/:id/fuel/history` | `view_telemetry` OR `view_fuel_record` | Fuel-level time series (≤31 d) |
| GET | `/api/vehicles/:id/fuel/events` | `view_telemetry` OR `view_fuel_record` | Refuel/drop events with confidence |
| GET | `/api/vehicles/:id/fuel/summary` | `view_telemetry` OR `view_fuel_record` | Distance / litres / km·L⁻¹ / counts (≤31 d) |

### 4.11 IoT device management

| Method | Path | Permission | Description |
|---|---|---|---|
| GET | `/api/iot/devices` | `manage_iot_device` | List registered devices |
| POST | `/api/iot/devices` | `manage_iot_device` | `{serial, label?, vehicleId?, issueKey?}` — returns plaintext apiKey **once** if `issueKey=true` |
| GET | `/api/iot/devices/:id` | `manage_iot_device` | Get one |
| PATCH | `/api/iot/devices/:id` | `manage_iot_device` | `{label?, vehicleId?, isActive?}` |
| DELETE | `/api/iot/devices/:id` | `manage_iot_device` | Remove |
| POST | `/api/iot/devices/:id/rotate-key` | `manage_iot_device` | Issue fresh apiKey (plaintext **once**) |
| POST | `/api/iot/devices/:id/test-ping` | `manage_iot_device` | Emit test ping |
| GET | `/api/iot/ingestion` | `manage_iot_device` | Ingestion contract (URLs, limits, sample JSON) |

### 4.12 Notifications & preferences

| Method | Path | Permission | Description |
|---|---|---|---|
| GET | `/api/notifications` | `view_notification` | List + unread count |
| POST | `/api/notifications/dismiss-all` | `change_notification` | Mark all read |
| POST | `/api/notifications/:id/seen` | `change_notification` | Mark one |
| GET | `/api/notifications/preferences` | authenticated | Current prefs |
| PUT | `/api/notifications/preferences` | authenticated | Update prefs |

### 4.13 Audit log

| Method | Path | Permission | Description |
|---|---|---|---|
| GET | `/api/audit` | `view_audit_entry` | Newest 500 |
| GET | `/api/audit/search` | `view_audit_entry` | Filters: `entity`, `refId`, `user`, `action`, `q`, `from`, `to` |

### 4.14 Admin

| Method | Path | Permission | Description |
|---|---|---|---|
| GET | `/api/admin/export` | `export_data` | Full JSON snapshot |
| POST | `/api/admin/import` | `import_data` | Replace collections, clears cache |
| POST | `/api/admin/reset` | `reset_data` | Wipe collections, clears cache |

---

## 5. Permissions Model

Fleet uses **Django-style codenames** stored in the JWT's `permissions` claim
under the `iag.fleet` namespace. Verbs per entity:

- `view_<entity>` — read access (`view_vehicle`, `view_driver`, …)
- `add_<entity>` — create
- `change_<entity>` — update / patch / workflow transition
- `delete_<entity>` — delete
- Custom codenames for non-CRUD verbs: `complete_toolbox_jmp`,
  `advance_stage_cargo`, `assign_request`, `simulate_vehicles`, etc.

Frontend should hide UI elements the caller doesn't have permission for —
inspect `permissions` from `GET /api/users/me` at sign-in and gate
components. Backend will still enforce; UI gating is for UX.

The full permission catalog is registered with the auth service at fleet
startup; see [auth's permissions registry](../../../../shared/services/authentication/internal/handlers/handlers.go).

---

## 6. Real-Time Streams (Server-Sent Events)

Fleet exposes three SSE endpoints. All return `text/event-stream` and require
the same Bearer auth as REST.

| Endpoint | Event name | Permission | Cadence |
|---|---|---|---|
| `GET /api/notifications/stream` | `bell` | `view_notification` | Push on every new/updated notification |
| `GET /api/vehicles/:id/track/stream` | `ping` | `view_telemetry` | Real-time + 2-second fallback poll |
| `GET /api/vehicles/live/stream` | `fleet` | `view_vehicle` OR `view_telemetry` | Snapshot every ~3 s |

### Frame shapes

```jsonc
// event: bell
{ "id": "...", "title": "...", "kind": "fuel_anomaly|maintenance_due|...",
  "createdAt": "2026-05-28T10:00:00Z", "seen": false }

// event: ping (single vehicle)
{ "ts": "2026-05-28T10:00:00Z", "lat": 0.31, "lng": 32.58,
  "speedKmh": 42, "heading": 187, "fuel": 0.83, "odo": 124501.4,
  "ignition": true, "battery": 12.6, "signal": 4, "location": "Kampala" }

// event: fleet (live map)
{ "generatedAt": "...", "vehicles": [
    { "id": "...", "plate": "UBC 123A", "lat": 0.31, "lng": 32.58,
      "status": "moving", "heading": 187, "location": "..." }, ...
] }
```

### Connecting from the browser

Native `EventSource` can't set `Authorization` headers cross-browser. Two
options:

**A. BFF proxy (recommended in production)** — terminate the EventSource at
your Next.js / SvelteKit server, attach the token there, and pipe the stream
to the browser.

```ts
// app/api/fleet/notifications/stream/route.ts (Next.js App Router example)
export const dynamic = "force-dynamic";
export async function GET(req: Request) {
  const upstream = await fetch(
    `${process.env.FLEET_API_INTERNAL_URL}/notifications/stream`,
    {
      headers: { Authorization: `Bearer ${await getServerToken()}` },
      cache: "no-store",
    },
  );
  return new Response(upstream.body, {
    headers: { "Content-Type": "text/event-stream" },
  });
}
```

**B. fetch + ReadableStream (works directly with auth header)** —
Chrome/Firefox/Safari all support custom `fetch` with stream parsing:

```ts
const res = await fetch(`${baseUrl}/notifications/stream`, {
  headers: { Authorization: `Bearer ${token}` },
});
const reader = res.body!.getReader();
const decoder = new TextDecoder();
let buffer = "";
while (true) {
  const { value, done } = await reader.read();
  if (done) break;
  buffer += decoder.decode(value, { stream: true });
  const events = buffer.split("\n\n");
  buffer = events.pop()!;
  for (const evt of events) handleSseFrame(evt);
}
```

### Reconnect

Both options should implement exponential backoff (1 s → 2 s → 4 s, cap at
30 s) and re-issue the request. Fleet does **not** support `Last-Event-ID` —
on reconnect, the client may receive a brief flurry of events already seen
for the bell stream; dedupe by `id`.

---

## 7. Error Conventions

Fleet returns RFC-style problem JSON for non-2xx:

```json
{ "error": "human-readable message", "code": "permission_denied" }
```

| Status | Meaning | Frontend action |
|---|---|---|
| 400 | Bad request body / validation | Show inline field error |
| 401 | Missing / invalid / expired token | Refresh; on second 401, re-login |
| 403 | Permission denied | Hide the UI control; show toast |
| 404 | Resource not found | Treat as soft state (deleted elsewhere?) |
| 409 | Conflict (e.g. version mismatch) | Re-fetch and retry |
| 422 | Domain validation (e.g. stage transition not allowed) | Show domain error |
| 500 | Server error | Generic toast + retry button |
| 503 | Dependency unavailable (DB / Kafka) | Show maintenance banner |

---

## 8. Telemetry vs Operational Data — Why Two Backends?

Fleet uses **two databases**:

1. **Operational** (`DATABASE_URL`) — relational tables for vehicles, drivers,
   cargo, requests, fuel ledger entries.
2. **Telemetry** (`TELEMETRY_DATABASE_URL`) — TimescaleDB instance for raw GPS
   pings (`telemetry_timeseries`), daily aggregates (`telemetry_daily`), and
   auto-detected fuel anomalies (`fuel_events`). **Fleet reads only**;
   Fleet_IoT services own writes.

API-wise the split is invisible from the frontend's perspective — all
endpoints in §4.10 query the telemetry DB; everything else queries operational.

Range caps to know about:
- GPS playback: ≤ **14 days**, ≤ **50 k rows** per request.
- Fuel history / events / summary: ≤ **31 days** per request.
- Fleet-wide anomalies: ≤ **31 days**.

For longer windows, paginate by `from`/`to` windows client-side.

---

## 9. Live-Update Patterns

The frontend has three coordination strategies depending on the data:

| Data | Pattern | Reason |
|---|---|---|
| Live vehicle position | SSE `/vehicles/:id/track/stream` | Sub-second updates needed |
| Fleet map (≤200 vehicles) | SSE `/vehicles/live/stream` | Server batches; saves N socket connections |
| Bell notifications | SSE `/notifications/stream` | Push needed; falls back to refetch on reconnect |
| Dashboard KPIs | Refetch every 30 s | Cache is server-side (30 s TTL); no point streaming |
| Analytics | Refetch on tab focus + every 60 s | Cache is 45 s TTL |
| CRUD tables | Optimistic update + refetch on success | Standard React Query / TanStack Query pattern |

---

## 10. What's Missing (Not Shipped Today)

If you hit any of these and need them, file an issue against the fleet repo:

- **No OpenAPI spec.** Routes are hand-registered in
  [`internal/router/router.go`](../internal/router/router.go) and per-entity
  handlers under `internal/handlers/`. A future pass will add `swag` or
  `oapi-codegen` annotations.
- **No multipart uploads.** All bodies are JSON. Document attachments today
  are URLs to external storage.
- **No GraphQL.** Calendar, dashboard, and analytics endpoints are
  pre-aggregated to avoid the N+1 query problem.
- **No `Last-Event-ID` on SSE.** Reconnect-with-replay is not implemented.
- **No `WebSocket` endpoint.** Use SSE.

---

## 11. Quickstart Checklist

For a new fleet frontend project:

- [ ] Set `NEXT_PUBLIC_FLEET_API_URL` and `NEXT_PUBLIC_AUTH_API_URL` (§2).
- [ ] Implement OAuth password-grant login against auth service.
- [ ] Store access token in memory; set up silent refresh (§1).
- [ ] Install `@iag/fleet-client` and instantiate (§3).
- [ ] After login, call `GET /api/users/me` → cache `permissions` for UI
      gating (§5).
- [ ] Build the dashboard around `GET /api/dashboard/summary` (refetch 30 s).
- [ ] Wire SSE for notifications + the live map (§6).
- [ ] For CRUD pages, use TanStack Query against the eleven standard
      operations per resource (§4.4–4.5).
- [ ] Handle 401 → refresh, 403 → hide control, 409 → re-fetch (§7).

---

## See Also

- [FRONTEND_CONTRACT.md](./FRONTEND_CONTRACT.md) — short contract / base URL
  table.
- [PLATFORM_INTEGRATION.md](./PLATFORM_INTEGRATION.md) — backend deployment
  + env config.
- Auth service's `/oauth/token` — see
  [shared/services/authentication](../../../../shared/services/authentication).
- Shared TS client —
  [packages/fleet-client/src/index.ts](../../../../packages/fleet-client/src/index.ts).
