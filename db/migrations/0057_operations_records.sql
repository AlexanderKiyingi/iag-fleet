-- 0057: give the eight operations forms a table, and add the carrier master
--
-- The frontend ships read-write screens for weighbridge tickets, vehicle
-- diagnostics, hours-of-service, driver safety scores, fuel-card
-- reconciliation, service reminders, emissions and route/ETA — and this
-- service had no table for any of them. Every row those forms saved went to
-- the shared Go API's generic record store, which the platform cannot read;
-- with the adapter on, the screens rendered and the saves went nowhere the
-- platform could see. The Logistics app's shipment form also picks a carrier,
-- and cargo carries a free-text `transporter` with no master behind it.
--
-- Nine plain tables, each registered as a generic CRUD resource in
-- internal/router/router.go. Foreign keys to vehicles / drivers / jmps / cargo
-- are nullable and SET NULL on delete: the form does not require them, and a
-- ticket typed against a truck that has since left the register is still a
-- ticket. Derived columns (overweight, discrepancy, co2e_kg,
-- co2e_per_tonne_km) are written by the resource hooks, never by the client.
--
-- Same rules as 0044 and 0049: additive, IF NOT EXISTS throughout, DATE
-- columns nullable so an empty string casts to NULL rather than failing the
-- insert, and the 0050 touch_row trigger plus updated_at index on every table
-- so they behave like every other collection from day one.

CREATE TABLE IF NOT EXISTS weighbridge_tickets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference       TEXT NOT NULL DEFAULT '',
    date            DATE,
    cargo_id        UUID REFERENCES cargo(id)    ON DELETE SET NULL,
    vehicle_id      UUID REFERENCES vehicles(id) ON DELETE SET NULL,
    site            TEXT NOT NULL DEFAULT '',
    stage           TEXT NOT NULL DEFAULT '',
    gross_weight_kg DOUBLE PRECISION NOT NULL DEFAULT 0,
    axle_weight_kg  DOUBLE PRECISION,
    gross_limit_kg  DOUBLE PRECISION,
    axle_limit_kg   DOUBLE PRECISION,
    overweight      BOOLEAN NOT NULL DEFAULT FALSE,
    status          TEXT NOT NULL DEFAULT 'ok',
    notes           TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS weighbridge_tickets_vehicle_idx ON weighbridge_tickets (vehicle_id);
CREATE INDEX IF NOT EXISTS weighbridge_tickets_cargo_idx   ON weighbridge_tickets (cargo_id);

CREATE TABLE IF NOT EXISTS vehicle_diagnostics (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference        TEXT NOT NULL DEFAULT '',
    date             DATE,
    vehicle_id       UUID REFERENCES vehicles(id) ON DELETE SET NULL,
    code             TEXT NOT NULL DEFAULT '',
    description      TEXT NOT NULL DEFAULT '',
    severity         TEXT NOT NULL DEFAULT 'info',
    suggested_action TEXT NOT NULL DEFAULT '',
    work_order       TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'open',
    notes            TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS vehicle_diagnostics_vehicle_idx ON vehicle_diagnostics (vehicle_id);

CREATE TABLE IF NOT EXISTS driver_hos_logs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id        UUID REFERENCES drivers(id)  ON DELETE SET NULL,
    vehicle_id       UUID REFERENCES vehicles(id) ON DELETE SET NULL,
    jmp_id           UUID REFERENCES jmps(id)     ON DELETE SET NULL,
    date             DATE,
    driving_hours    DOUBLE PRECISION NOT NULL DEFAULT 0,
    rest_hours       DOUBLE PRECISION,
    continuous_hours DOUBLE PRECISION,
    status           TEXT NOT NULL DEFAULT 'ok',
    notes            TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS driver_hos_logs_driver_idx ON driver_hos_logs (driver_id, date DESC);

CREATE TABLE IF NOT EXISTS driver_safety_scores (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id        UUID REFERENCES drivers(id) ON DELETE SET NULL,
    period_start     DATE,
    period_end       DATE,
    score            DOUBLE PRECISION NOT NULL DEFAULT 0,
    harsh_count      INTEGER,
    supervisor       TEXT NOT NULL DEFAULT '',
    coaching_outcome TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'open',
    notes            TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS driver_safety_scores_driver_idx ON driver_safety_scores (driver_id, period_start DESC);

CREATE TABLE IF NOT EXISTS fuel_card_reconciliations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference         TEXT NOT NULL DEFAULT '',
    date              DATE,
    account           TEXT NOT NULL DEFAULT '',
    statement_balance DOUBLE PRECISION NOT NULL DEFAULT 0,
    system_balance    DOUBLE PRECISION NOT NULL DEFAULT 0,
    discrepancy       DOUBLE PRECISION NOT NULL DEFAULT 0,
    status            TEXT NOT NULL DEFAULT 'open',
    notes             TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS service_reminders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vehicle_id  UUID REFERENCES vehicles(id) ON DELETE SET NULL,
    code        TEXT NOT NULL DEFAULT '',
    date        DATE,
    due_date    DATE,
    source      TEXT NOT NULL DEFAULT 'pm-schedule',
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'open',
    notes       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS service_reminders_vehicle_idx ON service_reminders (vehicle_id, due_date);

CREATE TABLE IF NOT EXISTS emissions_entries (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference         TEXT NOT NULL DEFAULT '',
    date              DATE,
    vehicle_id        UUID REFERENCES vehicles(id) ON DELETE SET NULL,
    jmp_id            UUID REFERENCES jmps(id)     ON DELETE SET NULL,
    litres            DOUBLE PRECISION NOT NULL DEFAULT 0,
    km                DOUBLE PRECISION,
    tonnes            DOUBLE PRECISION,
    co2e_kg           DOUBLE PRECISION NOT NULL DEFAULT 0,
    co2e_per_tonne_km DOUBLE PRECISION,
    status            TEXT NOT NULL DEFAULT 'draft',
    notes             TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS emissions_entries_vehicle_idx ON emissions_entries (vehicle_id, date DESC);

CREATE TABLE IF NOT EXISTS route_etas (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference          TEXT NOT NULL DEFAULT '',
    jmp_id             UUID REFERENCES jmps(id)     ON DELETE SET NULL,
    vehicle_id         UUID REFERENCES vehicles(id) ON DELETE SET NULL,
    from_location      TEXT NOT NULL DEFAULT '',
    to_location        TEXT NOT NULL DEFAULT '',
    remaining_km       DOUBLE PRECISION,
    speed_kmh          DOUBLE PRECISION,
    suggested_route    TEXT NOT NULL DEFAULT '',
    live_eta           TEXT NOT NULL DEFAULT '',
    border_delay_hours DOUBLE PRECISION,
    status             TEXT NOT NULL DEFAULT 'planned',
    notes              TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS route_etas_jmp_idx ON route_etas (jmp_id);

CREATE TABLE IF NOT EXISTS carriers (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL,
    code           TEXT NOT NULL DEFAULT '',
    phone          TEXT NOT NULL DEFAULT '',
    email          TEXT NOT NULL DEFAULT '',
    contact_person TEXT NOT NULL DEFAULT '',
    address        TEXT NOT NULL DEFAULT '',
    type           TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'active',
    notes          TEXT NOT NULL DEFAULT ''
);

-- Record timestamps and the touch trigger, exactly as 0050 gives every other
-- domain table. touch_row() exists from 0050.
DO $$
DECLARE
    t TEXT;
    new_tables TEXT[] := ARRAY[
        'weighbridge_tickets', 'vehicle_diagnostics', 'driver_hos_logs',
        'driver_safety_scores', 'fuel_card_reconciliations', 'service_reminders',
        'emissions_entries', 'route_etas', 'carriers'
    ];
BEGIN
    FOREACH t IN ARRAY new_tables LOOP
        EXECUTE format(
            'ALTER TABLE %I ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()', t);
        EXECUTE format(
            'ALTER TABLE %I ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()', t);
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', t || '_touch', t);
        EXECUTE format(
            'CREATE TRIGGER %I BEFORE INSERT OR UPDATE ON %I '
            'FOR EACH ROW EXECUTE FUNCTION touch_row()', t || '_touch', t);
        EXECUTE format(
            'CREATE INDEX IF NOT EXISTS %I ON %I (updated_at DESC)', t || '_updated_at_idx', t);
    END LOOP;
END;
$$;
