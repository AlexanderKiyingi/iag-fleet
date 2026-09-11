-- 0055: range-partition telemetry_timeseries by month, when TimescaleDB is not
-- available.
--
-- 0010 already converts this table to a hypertable IF the timescaledb extension
-- is installed. On the deployed database it is not installed and cannot be —
-- `pg_available_extensions` does not list it, so it needs a different Postgres
-- image, not a CREATE EXTENSION. That left the table plain, and the one thing
-- that really hurts on a plain table is retention: PurgeBefore is a DELETE, and
-- on a large append-only table a DELETE is a long transaction that leaves bloat
-- behind for autovacuum on a database twenty-one other schemas share.
--
-- Native range partitioning (PostgreSQL 12+) gives that part back. Retention
-- becomes DROP TABLE on a month, which is instant and leaves nothing to vacuum,
-- and time-range reads prune to the months they touch.
--
-- It does not give compression or continuous aggregates. Those need Timescale,
-- and this does not block moving there later: a hypertable can be built from a
-- partitioned table's data whenever a Timescale instance exists.
--
-- Done now because the table is 264 kB. The conversion copies every row, so
-- this is the cheapest it will ever be.

DO $partition$
DECLARE
    target_schema TEXT;
    existing      REGCLASS;
    is_parted     BOOLEAN;
    row_count     BIGINT;
    first_month   DATE;
    last_month    DATE;
    m             DATE;
BEGIN
    existing := to_regclass('telemetry_timeseries');
    IF existing IS NULL THEN
        RAISE NOTICE '0055: no telemetry_timeseries on the search_path — nothing to partition';
        RETURN;
    END IF;

    -- Operate on wherever the table actually resolves. Fleet's tables are
    -- part-way through a move out of public, so hardcoding either schema would
    -- be wrong in one of the two environments this has to run in.
    SELECT n.nspname, c.relkind = 'p'
      INTO target_schema, is_parted
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE c.oid = existing;

    IF is_parted THEN
        RAISE NOTICE '0055: %.telemetry_timeseries is already partitioned', target_schema;
        RETURN;
    END IF;

    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        -- 0010 owns the hypertable path. Two partitioning schemes on one table
        -- is not a thing, and Timescale's is the better one.
        RAISE NOTICE '0055: timescaledb present — leaving 0010''s hypertable alone';
        RETURN;
    END IF;

    EXECUTE format('SELECT count(*) FROM %I.telemetry_timeseries', target_schema) INTO row_count;
    RAISE NOTICE '0055: converting %.telemetry_timeseries (% rows) to monthly partitions', target_schema, row_count;

    -- Rename rather than drop. If anything below is wrong the data is still
    -- sitting there under a name someone can find, which matters more than
    -- tidiness on a table holding the only copy of vehicle history.
    EXECUTE format('ALTER TABLE %I.telemetry_timeseries RENAME TO telemetry_timeseries_preparted', target_schema);

    EXECUTE format($ddl$
        CREATE TABLE %I.telemetry_timeseries (
            id          BIGSERIAL NOT NULL,
            vehicle_id  TEXT NOT NULL,
            device_id   BIGINT,
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
        ) PARTITION BY RANGE (ts)
    $ddl$, target_schema);

    -- A DEFAULT partition is not optional here. Without it an INSERT for a month
    -- nobody pre-created fails outright, and that failure lands in the ingest
    -- path: Pipeline.Ingest returns before the hot-state update, so a missing
    -- partition would stop the live map as well as history. With it, a late
    -- ping is merely in the wrong place, and the maintenance job relocates it.
    EXECUTE format(
        'CREATE TABLE %I.telemetry_timeseries_default PARTITION OF %I.telemetry_timeseries DEFAULT',
        target_schema, target_schema);

    -- Cover every month the existing data spans, plus a year ahead so nothing
    -- depends on the maintenance job having run before the next ping arrives.
    EXECUTE format('SELECT date_trunc(''month'', min(ts))::date, date_trunc(''month'', max(ts))::date
                      FROM %I.telemetry_timeseries_preparted', target_schema)
       INTO first_month, last_month;
    first_month := LEAST(COALESCE(first_month, date_trunc('month', now())::date),
                         date_trunc('month', now())::date);
    last_month  := GREATEST(COALESCE(last_month, date_trunc('month', now())::date),
                            (date_trunc('month', now()) + INTERVAL '12 months')::date);

    m := first_month;
    WHILE m <= last_month LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I.%I PARTITION OF %I.telemetry_timeseries
               FOR VALUES FROM (%L) TO (%L)',
            target_schema,
            'telemetry_timeseries_' || to_char(m, 'YYYY_MM'),
            target_schema,
            m,
            (m + INTERVAL '1 month')::date);
        m := (m + INTERVAL '1 month')::date;
    END LOOP;

    EXECUTE format(
        'INSERT INTO %I.telemetry_timeseries
            (vehicle_id, device_id, ts, lat, lng, altitude, heading, speed_kmh,
             satellites, odo, fuel_level, ignition, event_id, raw)
         SELECT vehicle_id, device_id, ts, lat, lng, altitude, heading, speed_kmh,
                satellites, odo, fuel_level, ignition, event_id, raw
           FROM %I.telemetry_timeseries_preparted',
        target_schema, target_schema);

    -- The unique index is what ON CONFLICT (vehicle_id, ts) relies on, and a
    -- unique index on a partitioned table must contain the partition key. It
    -- already does, which is the only reason this conversion is possible
    -- without changing the ingest contract.
    EXECUTE format(
        'CREATE UNIQUE INDEX telemetry_timeseries_vehicle_ts_uidx
           ON %I.telemetry_timeseries (vehicle_id, ts)', target_schema);
    EXECUTE format(
        'CREATE INDEX telemetry_timeseries_vehicle_ts_idx
           ON %I.telemetry_timeseries (vehicle_id, ts DESC)', target_schema);
    EXECUTE format(
        'CREATE INDEX telemetry_timeseries_ts_brin_idx
           ON %I.telemetry_timeseries USING BRIN (ts)', target_schema);

    RAISE NOTICE '0055: done — old rows kept in %.telemetry_timeseries_preparted until someone drops it', target_schema;
END $partition$;
