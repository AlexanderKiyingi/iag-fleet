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
    old_index     TEXT;
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

    -- Renaming a table does NOT rename the things it owns. Its indexes and its
    -- BIGSERIAL sequence keep their original names, so recreating
    -- telemetry_timeseries below walks straight into them:
    -- telemetry_timeseries_vehicle_ts_uidx already exists (SQLSTATE 42P07), and
    -- the migration dies. On this service that is not a failed migration, it is
    -- an outage — autoMigrate refuses to serve when a migration fails, so the
    -- whole fleet API stays down until someone renames an index by hand.
    FOR old_index IN
        SELECT c.relname
          FROM pg_index i
          JOIN pg_class c ON c.oid = i.indexrelid
          JOIN pg_class t ON t.oid = i.indrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE t.relname = 'telemetry_timeseries_preparted'
           AND n.nspname = target_schema
    LOOP
        -- left(…, 52) keeps the result inside the 63-character identifier limit;
        -- the names in play are far shorter, so this only ever matters to a name
        -- someone adds later.
        EXECUTE format('ALTER INDEX %I.%I RENAME TO %I',
                       target_schema, old_index, left(old_index, 52) || '_preparted');
    END LOOP;

    -- The sequence would not error — serial picks a free name, quietly landing
    -- on telemetry_timeseries_id_seq1 — but then the canonical name belongs to
    -- the dead table forever, which is a trap for whoever reads this next.
    IF to_regclass(format('%I.telemetry_timeseries_id_seq', target_schema)) IS NOT NULL THEN
        EXECUTE format(
            'ALTER SEQUENCE %I.telemetry_timeseries_id_seq RENAME TO telemetry_timeseries_preparted_id_seq',
            target_schema);
    END IF;

    EXECUTE format($ddl$
        CREATE TABLE %I.telemetry_timeseries (
            id          BIGSERIAL NOT NULL,
            vehicle_id  TEXT NOT NULL,
            -- Carried over from 0010. A partitioned table can be the
            -- referencing side of a foreign key (PG 12+), so dropping it here
            -- would have silently traded a constraint for nothing: deleting a
            -- device used to null out device_id, and without this it would
            -- leave rows pointing at a device that no longer exists.
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
