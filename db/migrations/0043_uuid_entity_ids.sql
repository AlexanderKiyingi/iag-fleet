-- 0043: Convert fleet surrogate entity ids from text to uuid
--
-- Fleet generates its ids with uuid.NewString(); the columns were declared TEXT.
-- Retyping them matches the value already stored and brings fleet in line with
-- finance, warehouse, ERP, production, procurement and CRM.
--
-- Fleet declares only two foreign keys on text columns, so nearly every
-- relationship here is a plain column. They are converted with the same mapping
-- as the ids they point at, or they would silently dangle.
--
-- WHAT IS DELIBERATELY NOT CONVERTED
--
--   telemetry_timeseries.vehicle_id  telemetry_timeseries becomes a TimescaleDB
--   telemetry_daily.vehicle_id       hypertable when the extension is present
--                                    (0010), with compression enabled in 0039.
--                                    Timescale refuses ALTER TYPE on a
--                                    compressed hypertable, and these tables can
--                                    live in a separate database entirely
--                                    (TELEMETRY_DATABASE_URL). The reference
--                                    crosses a store boundary, so it stays text.
--   notifications.ref_id             Polymorphic — paired with ref_type, and
--   audit_entries.ref_id             part of the (user_id, kind, ref_type,
--   task_items.source_id             ref_id) unique constraint. Paired with
--                                    entity and source respectively. These point
--                                    at any table, so no single mapping is
--                                    correct.
--   parts.warehouse_item_id          An id owned by the warehouse service.
--   *.user_id                        Auth subject strings from
--                                    auth.PlatformUserID. Owned by the
--                                    authentication service, and can be empty.
--   geofence_pois.name               Natural keys. operator_ticker is a
--   operator_ticker.id               single configuration row keyed by the
--                                    literal 'singleton' and read as WHERE id =
--                                    'singleton' in the store layer.
--   warehouse_event_dedupe.event_id  Upstream event ids.
--   fleet_event_outbox.id            bigint, read in id order by the relay.
--   fleet_api_audit.id, audit_entries.id, fuel_events.id, iot_devices.id,
--   device_commands.id, iot_status_observations.id   Log and device tables
--                                    keyed by bigint.
--   fleet_external_refs.*            Correlation columns, intentionally text.

-- A table rewrite takes ACCESS EXCLUSIVE. Without a timeout, an ALTER that
-- cannot get the lock immediately waits in the lock queue — and every read that
-- arrives behind it queues too, so a migration run against live traffic stalls
-- the service rather than just itself. Failing after ten seconds leaves the
-- transaction rolled back and the table untouched; re-run it in a quiet window.
SET LOCAL lock_timeout = '10s';

CREATE OR REPLACE FUNCTION fleet_id_to_uuid(v TEXT) RETURNS UUID AS $fn$
    SELECT CASE
        WHEN v IS NULL OR v = '' THEN NULL
        WHEN v ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN v::uuid
        -- RFC 4122 v3 shape (md5-based). Telemetry-detected trips carried ids
        -- like TRP-TEL-<vehicle>-<unix>; they map deterministically so parents
        -- and children land on the same value.
        ELSE uuid_in(overlay(overlay(md5('iag:fleet:' || v)
                 placing '3' from 13) placing '8' from 17)::cstring)
    END
$fn$ LANGUAGE sql IMMUTABLE;

-- ---- drop the foreign keys ------------------------------------------------
ALTER TABLE iot_devices             DROP CONSTRAINT iot_devices_vehicle_id_fkey;
ALTER TABLE vehicle_overspeed_state DROP CONSTRAINT vehicle_overspeed_state_vehicle_id_fkey;

-- ---- primary keys ---------------------------------------------------------
ALTER TABLE cargo               ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE cargo_docs          ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE compliance_items    ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE deployment_days     ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE drivers             ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE fuel_records        ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE fuel_requests       ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE inspection_templates ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE jmps                ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE maintenance_items   ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE notifications       ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE parts               ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE pm_schedules        ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE safety_events       ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE service_requests    ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE task_items          ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE trips               ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE tyres               ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE vehicle_inspections ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);
ALTER TABLE vehicles            ALTER COLUMN id TYPE UUID USING fleet_id_to_uuid(id);

-- ---- referencing columns --------------------------------------------------
ALTER TABLE cargo_docs        ALTER COLUMN cargo_id       TYPE UUID USING fleet_id_to_uuid(cargo_id);
ALTER TABLE compliance_items  ALTER COLUMN driver_id      TYPE UUID USING fleet_id_to_uuid(driver_id);
ALTER TABLE compliance_items  ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE device_commands   ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE drivers           ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE fuel_events       ALTER COLUMN fuel_record_id TYPE UUID USING fleet_id_to_uuid(fuel_record_id);
ALTER TABLE fuel_events       ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE fuel_records      ALTER COLUMN driver_id      TYPE UUID USING fleet_id_to_uuid(driver_id);
ALTER TABLE fuel_records      ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE fuel_requests     ALTER COLUMN driver_id      TYPE UUID USING fleet_id_to_uuid(driver_id);
ALTER TABLE fuel_requests     ALTER COLUMN fuel_record_id TYPE UUID USING fleet_id_to_uuid(fuel_record_id);
ALTER TABLE fuel_requests     ALTER COLUMN jmp_id         TYPE UUID USING fleet_id_to_uuid(jmp_id);
ALTER TABLE fuel_requests     ALTER COLUMN request_id     TYPE UUID USING fleet_id_to_uuid(request_id);
ALTER TABLE fuel_requests     ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE iot_devices       ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE jmps              ALTER COLUMN driver_id      TYPE UUID USING fleet_id_to_uuid(driver_id);
ALTER TABLE jmps              ALTER COLUMN source_request_id TYPE UUID USING fleet_id_to_uuid(source_request_id);
ALTER TABLE jmps              ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE maintenance_items ALTER COLUMN linked_safety_id TYPE UUID USING fleet_id_to_uuid(linked_safety_id);
ALTER TABLE maintenance_items ALTER COLUMN pm_schedule_id TYPE UUID USING fleet_id_to_uuid(pm_schedule_id);
ALTER TABLE maintenance_items ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE pm_schedules      ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE safety_events     ALTER COLUMN driver_id      TYPE UUID USING fleet_id_to_uuid(driver_id);
ALTER TABLE safety_events     ALTER COLUMN linked_wo_id   TYPE UUID USING fleet_id_to_uuid(linked_wo_id);
ALTER TABLE safety_events     ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE service_requests  ALTER COLUMN assigned_driver_id  TYPE UUID USING fleet_id_to_uuid(assigned_driver_id);
ALTER TABLE service_requests  ALTER COLUMN assigned_vehicle_id TYPE UUID USING fleet_id_to_uuid(assigned_vehicle_id);
ALTER TABLE service_requests  ALTER COLUMN deployment_entry_id TYPE UUID USING fleet_id_to_uuid(deployment_entry_id);
ALTER TABLE service_requests  ALTER COLUMN jmp_id         TYPE UUID USING fleet_id_to_uuid(jmp_id);
ALTER TABLE service_requests  ALTER COLUMN task_id        TYPE UUID USING fleet_id_to_uuid(task_id);
ALTER TABLE trips             ALTER COLUMN driver_id      TYPE UUID USING fleet_id_to_uuid(driver_id);
ALTER TABLE trips             ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE tyres             ALTER COLUMN vehicle_id     TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE vehicle_geofence_state  ALTER COLUMN vehicle_id TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE vehicle_inspections ALTER COLUMN driver_id     TYPE UUID USING fleet_id_to_uuid(driver_id);
ALTER TABLE vehicle_inspections ALTER COLUMN maintenance_id TYPE UUID USING fleet_id_to_uuid(maintenance_id);
ALTER TABLE vehicle_inspections ALTER COLUMN template_id   TYPE UUID USING fleet_id_to_uuid(template_id);
ALTER TABLE vehicle_inspections ALTER COLUMN vehicle_id    TYPE UUID USING fleet_id_to_uuid(vehicle_id);
ALTER TABLE vehicle_overspeed_state ALTER COLUMN vehicle_id TYPE UUID USING fleet_id_to_uuid(vehicle_id);

-- ---- restore the foreign keys ---------------------------------------------
ALTER TABLE iot_devices ADD CONSTRAINT iot_devices_vehicle_id_fkey
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE SET NULL;
ALTER TABLE vehicle_overspeed_state ADD CONSTRAINT vehicle_overspeed_state_vehicle_id_fkey
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE;

-- ---- replace the trip-detection idempotency key ---------------------------
-- Telemetry trip detection used to mint a deterministic id,
-- TRP-TEL-<vehicle>-<unix>, so that re-running the job collided on the primary
-- key instead of inserting the trip twice. A uuid cannot carry that property, so
-- the guarantee moves into the schema where it belongs.
--
-- The job's own guard (tripExistsJob) matches on vehicle and start time within a
-- minute and remains the primary defence; this index is the backstop for two
-- runs racing each other. It is partial because only auto-generated trips carry
-- the guarantee — a dispatcher may legitimately record two manual trips for one
-- vehicle at the same timestamp.
CREATE UNIQUE INDEX IF NOT EXISTS trips_autogen_vehicle_started_idx
    ON trips (vehicle_id, started_at)
    WHERE auto_generated AND started_at IS NOT NULL;

-- ---- the database mints ids from here on ----------------------------------
ALTER TABLE cargo                ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE cargo_docs           ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE compliance_items     ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE deployment_days      ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE drivers              ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE fuel_records         ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE fuel_requests        ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE inspection_templates ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE jmps                 ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE maintenance_items    ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE notifications        ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE parts                ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE pm_schedules         ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE safety_events        ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE service_requests     ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE task_items           ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE trips                ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE tyres                ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE vehicle_inspections  ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE vehicles             ALTER COLUMN id SET DEFAULT gen_random_uuid();

DROP FUNCTION fleet_id_to_uuid(TEXT);
