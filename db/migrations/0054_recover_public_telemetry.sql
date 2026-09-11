-- 0054: bring back telemetry written into `public` instead of `iag_fleet`.
--
-- The services share one database and separate by schema. The telemetry gateway
-- took its schema from a ?search_path= DSN param and, when that param was
-- missing, wrote pings to public.telemetry_timeseries while this service read
-- iag_fleet.telemetry_timeseries. The table exists in both, so nothing errored:
-- every insert succeeded, every read returned an empty array, and a vehicle
-- reporting every twenty seconds had no history at all.
--
-- The gateway now pins its search_path in code and refuses to start if its
-- pings table resolves anywhere unexpected. This recovers what was written
-- before that.
--
-- COPY, NOT MOVE. Nothing is deleted here. If the column sets have drifted
-- between the two copies, or some of these rows are duplicates of good ones,
-- the worst case is that this does less than hoped — not that history is lost.
-- Dropping public.telemetry_timeseries is a separate decision for after someone
-- has looked at the row counts, and it does not belong in the same migration as
-- the copy.
--
-- Safe on a database that never had the problem: everything below is guarded on
-- the table existing, so it is a no-op there.

DO $$
DECLARE
    moved   BIGINT := 0;
    present BIGINT := 0;
BEGIN
    IF to_regclass('public.telemetry_timeseries') IS NULL THEN
        RAISE NOTICE 'no public.telemetry_timeseries — nothing to recover';
        RETURN;
    END IF;

    -- Same relation reached two ways (a search_path that resolves iag_fleet to
    -- public) would make this copy a table into itself. Nothing good follows.
    IF to_regclass('public.telemetry_timeseries') = to_regclass('iag_fleet.telemetry_timeseries') THEN
        RAISE NOTICE 'public and iag_fleet resolve to the same relation — nothing to do';
        RETURN;
    END IF;

    SELECT count(*) INTO present FROM public.telemetry_timeseries;
    IF present = 0 THEN
        RAISE NOTICE 'public.telemetry_timeseries is empty — nothing to recover';
        RETURN;
    END IF;

    -- Columns listed explicitly rather than SELECT *. The two tables were
    -- created by the same migration, but a stranded copy is by definition one
    -- this service stopped managing, so its shape cannot be assumed. `id` is
    -- deliberately omitted: it is a BIGSERIAL identity local to each table, and
    -- carrying it over would collide with rows already here.
    INSERT INTO iag_fleet.telemetry_timeseries
        (vehicle_id, device_id, ts, lat, lng, altitude, heading, speed_kmh,
         satellites, odo, fuel_level, ignition, event_id, raw)
    SELECT t.vehicle_id,
           -- device_id carries an FK onto iot_devices. A stranded row may point
           -- at a device id that does not exist in THIS schema, and an FK
           -- violation here is an outage: autoMigrate refuses to serve when a
           -- migration fails. Resolved through a scalar subquery so an unknown
           -- device becomes NULL (the column is nullable, and ON DELETE SET NULL
           -- says the schema already treats that as acceptable) instead of
           -- taking the service down over provenance nobody will miss.
           (SELECT d.id FROM iag_fleet.iot_devices d WHERE d.id = t.device_id),
           t.ts, t.lat, t.lng, t.altitude, t.heading, t.speed_kmh,
           t.satellites, t.odo, t.fuel_level, t.ignition, t.event_id, t.raw
    FROM public.telemetry_timeseries t
    ON CONFLICT (vehicle_id, ts) DO NOTHING;

    GET DIAGNOSTICS moved = ROW_COUNT;
    RAISE NOTICE 'recovered % of % rows from public.telemetry_timeseries (the rest were already present)',
        moved, present;
END $$;
