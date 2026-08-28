# Getting telemetry flowing

As of 2026-08-28 the fleet has **42 vehicles, one GPS fix between them** (last
ping 2026-08-03) and **zero registered devices**. Nothing in the code path is
broken — the whole pipeline is built and tested. What is missing is an address a
device can dial.

## What exists

`Fleet_IoT` (`edge/Fleet_IoT`, repo `iag-telemetry-gateway`) ships three
listeners, each its own binary:

| Binary | Default port | Protocol |
|--------|--------------|----------|
| `/app/ingest` | `:4080` | HTTP JSON — `POST /v1/pings`, Bearer = the device's plaintext API key |
| `/app/gateway` | `:5027` | Teltonika Codec 8 / 8E, TCP |
| `/app/sinotrack` | `:5013` | SinoTrack / HQ protocol, TCP (ST-901/906/915 and GT06-era clones) |

Pings land in `telemetry_timeseries` (TimescaleDB hypertable). `iag-fleet` reads
them back — `/api/vehicles/:id/track`, `/track/latest`, `/track/stream` and the
fleet-wide `/api/vehicles/live/stream` — and `ApplyVehicleHotState` keeps
`vehicles.lat/lng/heading/speed/last_seen` current so the map has a position
without opening a stream.

## What is missing

**A publicly reachable address.** All three listeners are private. A tracker on
a GPRS SIM cannot reach a `*.railway.internal` host, and the HTTP ingest has no
public route either.

**`TELEMETRY_INGEST_URL` on the fleet service.** `GET /api/iot/ingestion` is
what an operator reads to find where to send data. Until this is set it reports
`"configured": false` and gives no URL — it used to fall back to the
compose-internal `http://fleet-iot-ingest:4080`, which looked actionable and
was not.

## Steps

1. **Deploy the ingest listeners** with public ingress:
   - HTTP (`/app/ingest`): a normal public HTTPS domain is enough.
   - TCP (`/app/gateway`, `/app/sinotrack`): these need a **TCP proxy**, not an
     HTTP route. Each needs `DATABASE_URL` pointed at the telemetry schema.

   The platform runs **one database** shared across services. Fleet uses two
   schemas in it: `iag_fleet` for the relational tables (connections set
   `search_path = "iag_fleet, public"`) and a separate schema for the
   time-series data. `TELEMETRY_DATABASE_URL` exists so telemetry *can* be
   split onto its own host later; it is not a second database today.
2. **Set `TELEMETRY_INGEST_URL`** on `iag-fleet` to the public HTTPS base of the
   ingest service — no trailing `/v1/pings`, the guide appends it. Confirm with
   `GET /api/v1/fleet/api/iot/ingestion`: `"configured"` must be `true`.
3. **Register a device**: `POST /api/iot/devices { serial, label, vehicleId,
   issueKey: true }`. `serial` must be the tracker's IMEI — the TCP listeners
   identify a device by IMEI and there is no bearer token on the wire. The
   plaintext API key is returned **once**; only its SHA-256 digest is stored.
   `POST /api/iot/devices/:id/rotate-key` issues a new one and stops the old.
4. **Program the tracker.** For the ST-901 see
   `edge/Fleet_IoT/docs/ST-901-onboarding.md`. Note the caveat there: most
   ST-901 firmwares want a literal **IP address**, and a Railway TCP proxy gives
   a *domain*:port. If the firmware will not take a hostname, terminate on
   something with a stable public IP.
5. **Verify** without waiting for hardware:
   `POST /api/iot/devices/:id/test-ping` inserts a real ping for the bound
   vehicle and syncs its hot state. Then `GET /api/vehicles/:id/track/latest`
   should return it, and the vehicle's `lat`/`lng`/`lastSeen` should move.
   The device must be assigned to a vehicle and active first.

## Checking it worked

    GET /api/v1/fleet/api/vehicles?limit=500

Count rows with a non-zero `lat`/`lng` and a recent `lastSeen`. `lastFixSource`
is written by `SyncVehicleFromPing` inside Fleet_IoT, so it is also the cheapest
signal that ingest is reaching the database at all — it is empty on all 42
vehicles today.
