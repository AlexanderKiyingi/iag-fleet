-- Telemetry ↔ fuel ledger link, DVIR-style inspections, preventive maintenance schedules.

-- Link auto-detected refuels to manual fuel_records (bidirectional).
ALTER TABLE fuel_events
    ADD COLUMN IF NOT EXISTS fuel_record_id TEXT;

ALTER TABLE fuel_records
    ADD COLUMN IF NOT EXISTS fuel_event_id BIGINT;

CREATE INDEX IF NOT EXISTS fuel_events_unlinked_refuel_idx
    ON fuel_events (vehicle_id, ts DESC)
    WHERE kind = 'refuel' AND fuel_record_id IS NULL;

CREATE INDEX IF NOT EXISTS fuel_records_unlinked_event_idx
    ON fuel_records (vehicle_id, date DESC)
    WHERE fuel_event_id IS NULL;

-- ─── Inspection templates (DVIR / periodic checklists) ─────────────────────

CREATE TABLE IF NOT EXISTS inspection_templates (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('pre-trip', 'post-trip', 'periodic')),
    checklist   JSONB NOT NULL DEFAULT '[]',
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    notes       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS inspection_templates_kind_idx ON inspection_templates (kind) WHERE active;

-- ─── Vehicle inspections (submitted DVIR forms) ──────────────────────────────

CREATE TABLE IF NOT EXISTS vehicle_inspections (
    id              TEXT PRIMARY KEY,
    template_id     TEXT NOT NULL,
    vehicle_id      TEXT NOT NULL,
    driver_id       TEXT,
    kind            TEXT NOT NULL CHECK (kind IN ('pre-trip', 'post-trip', 'periodic')),
    status          TEXT NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'submitted', 'passed', 'failed')),
    odo             DOUBLE PRECISION NOT NULL DEFAULT 0,
    location        TEXT,
    results         JSONB NOT NULL DEFAULT '[]',
    defects         JSONB NOT NULL DEFAULT '[]',
    signature       TEXT,
    submitted_at    TIMESTAMPTZ,
    submitted_by    TEXT,
    maintenance_id  TEXT,
    notes           TEXT
);

CREATE INDEX IF NOT EXISTS vehicle_inspections_vehicle_idx ON vehicle_inspections (vehicle_id, submitted_at DESC);
CREATE INDEX IF NOT EXISTS vehicle_inspections_status_idx ON vehicle_inspections (status);

-- ─── Preventive maintenance schedules ───────────────────────────────────────

CREATE TABLE IF NOT EXISTS pm_schedules (
    id                  TEXT PRIMARY KEY,
    vehicle_id          TEXT,
    name                TEXT NOT NULL,
    service_type        TEXT NOT NULL,
    service_description TEXT NOT NULL DEFAULT '',
    interval_km         DOUBLE PRECISION,
    interval_days       INTEGER,
    last_service_odo    DOUBLE PRECISION,
    last_service_date   DATE,
    next_due_km         DOUBLE PRECISION,
    next_due_date       DATE,
    vendor              TEXT,
    auto_create_wo      BOOLEAN NOT NULL DEFAULT TRUE,
    active              BOOLEAN NOT NULL DEFAULT TRUE,
    notes               TEXT
);

CREATE INDEX IF NOT EXISTS pm_schedules_vehicle_idx ON pm_schedules (vehicle_id) WHERE active;
CREATE INDEX IF NOT EXISTS pm_schedules_due_date_idx ON pm_schedules (next_due_date) WHERE active;

ALTER TABLE maintenance_items
    ADD COLUMN IF NOT EXISTS pm_schedule_id TEXT;

-- Default pre-trip DVIR template (Fleetio-style checklist categories).
INSERT INTO inspection_templates (id, name, kind, checklist, active, notes)
VALUES (
    'TPL-DVIR-PRE',
    'Pre-trip DVIR',
    'pre-trip',
    '[
      {"id":"brakes","label":"Brakes","category":"Safety","required":true},
      {"id":"steering","label":"Steering","category":"Safety","required":true},
      {"id":"lights","label":"Lights & reflectors","category":"Safety","required":true},
      {"id":"tyres","label":"Tyres & wheels","category":"Safety","required":true},
      {"id":"horn","label":"Horn","category":"Safety","required":true},
      {"id":"wipers","label":"Wipers / washers","category":"Safety","required":true},
      {"id":"mirrors","label":"Mirrors & glass","category":"Safety","required":true},
      {"id":"coupling","label":"Coupling / fifth wheel","category":"Cargo","required":false},
      {"id":"leaks","label":"Fluid leaks","category":"Mechanical","required":true},
      {"id":"emergency","label":"Emergency equipment","category":"Safety","required":true},
      {"id":"fire_ext","label":"Fire extinguisher","category":"Safety","required":true},
      {"id":"seatbelts","label":"Seat belts","category":"Safety","required":true}
    ]'::jsonb,
    TRUE,
    'Standard pre-trip driver vehicle inspection report'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO inspection_templates (id, name, kind, checklist, active, notes)
VALUES (
    'TPL-DVIR-POST',
    'Post-trip DVIR',
    'post-trip',
    '[
      {"id":"brakes","label":"Brakes","category":"Safety","required":true},
      {"id":"steering","label":"Steering","category":"Safety","required":true},
      {"id":"lights","label":"Lights","category":"Safety","required":true},
      {"id":"tyres","label":"Tyres","category":"Safety","required":true},
      {"id":"damage","label":"New damage noted","category":"Body","required":true},
      {"id":"leaks","label":"Fluid leaks","category":"Mechanical","required":true}
    ]'::jsonb,
    TRUE,
    'Post-trip inspection — note defects for next shift'
)
ON CONFLICT (id) DO NOTHING;
