-- Ensure telemetry_timeseries is a hypertable when the table lives outside
-- public (e.g. iag_fleet schema via role search_path). Migration 0012 only
-- checked table_schema = 'public', so existing platform volumes could skip
-- hypertable conversion.

DO $fleet_iot$
DECLARE
    tbl_schema text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        RAISE NOTICE 'timescaledb extension missing — skip hypertable schema fix';
        RETURN;
    END IF;

    SELECT table_schema INTO tbl_schema
    FROM information_schema.tables
    WHERE table_name = 'telemetry_timeseries'
    ORDER BY CASE WHEN table_schema = current_schema() THEN 0 ELSE 1 END
    LIMIT 1;

    IF tbl_schema IS NULL THEN
        RAISE NOTICE 'telemetry_timeseries not found — skip hypertable schema fix';
        RETURN;
    END IF;

    PERFORM create_hypertable(
        format('%I.%I', tbl_schema, 'telemetry_timeseries'),
        'ts',
        if_not_exists => TRUE,
        migrate_data => TRUE
    );
END
$fleet_iot$;
