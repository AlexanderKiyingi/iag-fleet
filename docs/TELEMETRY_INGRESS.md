# Getting telemetry flowing

As of 2026-08-28 the fleet has **42 vehicles, one GPS fix between them** (last
ping 2026-08-03) and **zero registered devices**. Nothing in the code path is
broken — the whole pipeline is built and tested.

An earlier revision of this document said what was missing was "a publicly
reachable address". For the **HTTP** path that is not true and has not been for
as long as the gateway has carried its fleet-iot route: the gateway is deployed,
already routes `/api/v1/fleet/api/iot/pings` to this service, and already lets
POST through unauthenticated so a device's own API key is what authenticates.
The public address exists. What is missing is a **process listening behind it**.

The **TCP** listeners are a different matter and do still need public ingress of
their own — see below.

## What exists

`Fleet_IoT` (`edge/Fleet_IoT`, repo `iag-telemetry-gateway`) ships three
listeners, each its own binary:

| Binary | Default port | Protocol |
|--------|--------------|----------|
| `/app/ingest` | `:4080` | HTTP JSON — `POST /api/iot/pings` (and `/v1/pings`), Bearer = the device's plaintext API key |
| `/app/gateway` | `:5027` | Teltonika Codec 8 / 8E, TCP |
| `/app/sinotrack` | `:5013` | SinoTrack / HQ protocol, TCP (ST-901/906/915 and GT06-era clones) |

Pings land in `telemetry_timeseries` (TimescaleDB hypertable). `iag-fleet` reads
them back — `/api/vehicles/:id/track`, `/track/latest`, `/track/stream` and the
fleet-wide `/api/vehicles/live/stream` — and `ApplyVehicleHotState` keeps
`vehicles.lat/lng/heading/speed/last_seen` current so the map has a position
without opening a stream.

## What is missing

**A running ingest process.** That is the whole of it for HTTP — no new domain,
no new public hostname.

The API gateway is already deployed and already fronts this. `iag-api-gateway`
registers a route *before* `/api/v1/fleet`:

```
/api/v1/fleet/api/iot/pings  →  UPSTREAM_FLEET_IOT_INGEST
rewritePrefix: /api/iot/pings
```

and a policy marking **POST on that prefix `public: true`** — so the gateway
does not demand a platform JWT. The device's own `Authorization: Bearer
<api-key>` passes through untouched and Fleet_IoT validates it against
`iot_devices`. `cmd/ingest` serves exactly `/api/iot/pings` (alongside
`/v1/pings`), which is the path the gateway rewrites to.

So the public device endpoint already exists as a route:

```
POST https://iag-api-gateway-production.up.railway.app/api/v1/fleet/api/iot/pings
```

It answers **502** today, which is the gateway saying `UPSTREAM_FLEET_IOT_INGEST`
is configured but nothing is listening behind it. Start the listener on the
private network and the 502 becomes a working endpoint.

**`TELEMETRY_INGEST_URL` on the fleet service.** `GET /api/iot/ingestion` is what
an operator reads to find where to send data. Until this is set it reports
`"configured": false` and gives no URL.

Note the guide advertises `base + /api/iot/pings`, not `/v1/pings`: the ingest
serves both, but only the former exists on the gateway, so it is the one that is
correct in either topology.

**What still genuinely needs public ingress: the TCP listeners.** `/app/gateway`
(Teltonika, :5027) and `/app/sinotrack` (SinoTrack/HQ, :5013) speak raw TCP.
An HTTP gateway cannot carry that, so those need a Railway TCP Proxy of their
own. Trackers that can post JSON over HTTP do not.

## Steps

1. **Deploy `/app/ingest` as a private Railway service.** `edge/Fleet_IoT/railway.toml`
   configures it: Dockerfile build, `/ready` healthcheck, default entrypoint.
   Give it **no public domain**. Set:

   ```
   DATABASE_URL=<telemetry schema>
   REGISTRY_DATABASE_URL=<same DSN as fleet DATABASE_URL>
   REDIS_URL=<optional, live fan-out>
   ```

   `REGISTRY_DATABASE_URL` is not optional in practice: without it the device
   API-key lookup and `SyncVehicleFromPing` have no operational DB, so pings
   store but `vehicles.lat/lng/last_seen` never move and the live map stays
   still. Leave `PORT` to Railway; do not also set `ADDR` or `PORT` is ignored
   and the healthcheck probes a dead port.

2. **Point the gateway at it.** On `iag-api-gateway`:

   ```
   UPSTREAM_FLEET_IOT_INGEST=http://<ingest-service>.railway.internal:4080
   ```

   Verify: `POST .../api/v1/fleet/api/iot/pings` with no body should return
   **401** (`missing Authorization: Bearer <api-key>`) rather than 502. A 401
   there is the success signal — it means the request reached the ingest.

3. **Set `TELEMETRY_INGEST_URL`** on `iag-fleet` to the gateway's fleet prefix:

   ```
   TELEMETRY_INGEST_URL=https://iag-api-gateway-production.up.railway.app/api/v1/fleet
   ```

   Confirm with `GET /api/v1/fleet/api/iot/ingestion`: `"configured"` must be
   `true` and `url` must read
   `.../api/v1/fleet/api/iot/pings`.

4. **Register a device**: `POST /api/iot/devices { serial, label, vehicleId,
   issueKey: true }`. `serial` must be the tracker's IMEI — the TCP listeners
   identify a device by IMEI and there is no bearer token on the wire. The
   plaintext API key is returned **once**; only its SHA-256 digest is stored.
   `POST /api/iot/devices/:id/rotate-key` issues a new one and stops the old.

5. **Verify** without waiting for hardware:
   `POST /api/iot/devices/:id/test-ping` inserts a real ping for the bound
   vehicle and syncs its hot state. Then `GET /api/vehicles/:id/track/latest`
   should return it, and the vehicle's `lat`/`lng`/`lastSeen` should move. The
   device must be assigned to a vehicle and active first.

   Or end-to-end through the gateway with the device's own key:

   ```sh
   curl -X POST      https://iag-api-gateway-production.up.railway.app/api/v1/fleet/api/iot/pings      -H "Authorization: Bearer <plaintext-api-key>"      -H "Content-Type: application/json"      -d '{"lat":0.3476,"lng":32.5825,"speedKmh":0}'
   ```

6. **Only then, the TCP listeners.** Two more services from the same image with
   `startCommand` set to `/app/gateway` and `/app/sinotrack`, each behind a
   Railway TCP Proxy. See `edge/Fleet_IoT/docs/ST-901-onboarding.md` — most
   ST-901 firmwares want a literal IP and a TCP Proxy gives a domain:port, so if
   the firmware will not take a hostname these have to terminate somewhere with
   a stable public IP.

## Checking it worked

    GET /api/v1/fleet/api/vehicles?limit=500

Count rows with a non-zero `lat`/`lng` and a recent `lastSeen`. `lastFixSource`
is written by `SyncVehicleFromPing` inside Fleet_IoT, so it is also the cheapest
signal that ingest is reaching the database at all — it is empty on all 42
vehicles today.
