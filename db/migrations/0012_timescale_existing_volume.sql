-- Upgrade databases that were created on plain postgres:16 before the
-- Timescale image/init was wired in. Safe to re-run: extension load and
-- hypertable creation are idempotent.
--
-- On Postgres installations where the timescaledb extension binary is NOT
-- available (e.g. Railway's managed Postgres without the Timescale add-on)
-- this migration is a no-op — telemetry_timeseries stays a regular heap
-- table, matching the fallback path in 0010.

DO $fleet_iot$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'timescaledb') THEN
        EXECUTE 'CREATE EXTENSION IF NOT EXISTS timescaledb CASCADE';
    ELSE
        RAISE NOTICE 'timescaledb extension not available on this Postgres — skipping hypertable conversion';
    END IF;
END
$fleet_iot$;

DO $fleet_iot$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')
       AND EXISTS (
           SELECT 1 FROM information_schema.tables
           WHERE table_schema = 'public' AND table_name = 'telemetry_timeseries'
       )
    THEN
        PERFORM create_hypertable(
            'telemetry_timeseries',
            'ts',
            if_not_exists => TRUE,
            migrate_data => TRUE
        );
    ELSE
        RAISE NOTICE 'timescaledb or telemetry_timeseries missing — skip hypertable';
    END IF;
END
$fleet_iot$;

-- Legacy heap table should already be gone after 0010; safety for partial upgrades.
DROP TABLE IF EXISTS telemetry_pings CASCADE;
