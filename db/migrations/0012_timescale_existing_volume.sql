-- Upgrade databases that were created on plain postgres:16 before Timescale image/init.
-- Safe to re-run: extension and hypertable calls are idempotent.

CREATE EXTENSION IF NOT EXISTS timescaledb CASCADE;

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
