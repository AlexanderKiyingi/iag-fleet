# Fleet frontend integration contract

The fleet UI (Next.js or other) should use the **platform API gateway** in all non-local scenarios.

## Base URL

| Environment | API base |
|-------------|----------|
| Local (gateway) | `http://localhost:8080/api/v1/fleet/api` |
| Production | `https://<gateway-host>/api/v1/fleet/api` |

Set `NEXT_PUBLIC_FLEET_API_URL` to the value above (include `/api` suffix).

## Authentication

1. Obtain an access token from authentication:

   `POST /api/v1/authentication/oauth/token`

2. Send on every API request:

   ```
   Authorization: Bearer <access_token>
   ```

3. Register for in-app notifications (bell) once after login:

   `GET /api/v1/fleet/api/users/me`

   This registers the user in `notification_recipients` for platform-mode bell fan-out.

4. Profile and permissions: use authentication `GET /api/v1/authentication/v1/users/me` or fleet `users/me` above.

## TypeScript client (`@iag/fleet-client`)

```typescript
import { FleetClient, fleetApiBaseFromEnv } from "@iag/fleet-client";

const fleet = new FleetClient({
  baseUrl: fleetApiBaseFromEnv(),
  getAccessToken: () => accessToken,
});

await fleet.me();
await fleet.listVehicles();
await fleet.markNotificationSeen("NTF-abc");
```

## SSE (live bell / vehicle tracks)

`EventSource` cannot set `Authorization` in all browsers. Options:

1. **BFF route** (recommended): Next.js API route proxies SSE and attaches the Bearer token server-side.
2. **Query token** (dev only): pass short-lived token as `?access_token=` — document security trade-offs.

Endpoints:

- `GET /api/v1/fleet/api/notifications/stream` — bell SSE (`event: bell`)
- `GET /api/v1/fleet/api/vehicles/:id/track/stream` — GPS SSE (`event: ping`)
- `GET /api/v1/fleet/api/vehicles/live/stream` — fleet map SSE (`event: fleet`)

## JSON shapes

Unchanged from the HAULA Fleet API — field names remain camelCase (`vehicleId`, `jmpId`, etc.). Only the base URL and auth mechanism change.

## Permissions (UI gating)

Use permissions from the token / `users/me` response. Platform codenames use the `fleet.*` prefix, e.g.:

- `fleet.view_vehicle`
- `fleet.change_jmp`
- `fleet.view_notification`

The API accepts both `fleet.view_vehicle` and unprefixed `view_vehicle` aliases in permission checks.
