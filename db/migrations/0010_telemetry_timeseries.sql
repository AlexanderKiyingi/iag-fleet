-- Fleet IoT: TimescaleDB hypertable for high-volume telemetry (replaces telemetry_pings).

CREATE TABLE IF NOT EXISTS telemetry_timeseries (
    id          BIGSERIAL NOT NULL,
    vehicle_id  TEXT NOT NULL,
    device_id   BIGINT REFERENCES iot_devices(id) ON DELETE SET NULL,
    ts          TIMESTAMPTZ NOT NULL,
    lat         DOUBLE PRECISION NOT NULL,
    lng         DOUBLE PRECISION NOT NULL,
    altitude    DOUBLE PRECISION,
    heading     DOUBLE PRECISION,
    speed_kmh   DOUBLE PRECISION,
    satellites  SMALLINT,
    odo         DOUBLE PRECISION,
    fuel_level  DOUBLE PRECISION,
    ignition    BOOLEAN,
    event_id    INTEGER,
    raw         JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS telemetry_timeseries_vehicle_ts_idx
    ON telemetry_timeseries (vehicle_id, ts DESC);

CREATE INDEX IF NOT EXISTS telemetry_timeseries_ts_brin_idx
    ON telemetry_timeseries USING BRIN (ts);

-- Convert to hypertable when TimescaleDB is available (deploy/postgres init enables extension).
DO $fleet_iot$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        PERFORM create_hypertable(
            'telemetry_timeseries',
            'ts',
            if_not_exists => TRUE,
            migrate_data => FALSE
        );
    ELSE
        RAISE NOTICE 'timescaledb extension not installed — telemetry_timeseries remains a regular table';
    END IF;
END
$fleet_iot$;

-- One-time backfill from legacy table.
DO $fleet_iot$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'telemetry_pings'
    ) THEN
        INSERT INTO telemetry_timeseries (
            id, vehicle_id, device_id, ts, lat, lng, altitude, heading, speed_kmh,
            satellites, odo, fuel_level, ignition, event_id, raw
        )
        OVERRIDING SYSTEM VALUE
        SELECT
            id, vehicle_id, device_id, ts, lat, lng, altitude, heading, speed_kmh,
            satellites, odo, fuel_level, ignition, event_id, raw
        FROM telemetry_pings;
        DROP TABLE telemetry_pings;
    END IF;
END
$fleet_iot$;
