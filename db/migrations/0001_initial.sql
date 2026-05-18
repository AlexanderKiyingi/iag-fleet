-- HAULA Fleet · Postgres schema
--
-- Mirrors the TypeScript domain in lib/types.ts. Most cross-entity columns are
-- stored as plain TEXT references (no foreign keys) on purpose: the frontend
-- seed has dangling references (vehicles pointing at drivers that don't exist,
-- service requests pointing at JMPs that aren't seeded, deployment entries
-- referencing vehicles outside the seed). Adding FKs up front would block the
-- initial load. Add `ALTER TABLE ... ADD CONSTRAINT ... NOT VALID` later, after
-- the data set is reconciled.
--
-- Column naming: snake_case in the database; the API layer maps to/from the
-- camelCase JSON the frontend expects.
--
-- Migration runner wraps the body in a transaction; do NOT add BEGIN/COMMIT
-- here. To add new schema, create db/migrations/000N_<description>.sql.

-- pgcrypto provides crypt()/gen_salt('bf') for bcrypt password hashing in
-- SQL. Go's golang.org/x/crypto/bcrypt verifies the resulting $2a$ hashes.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ─────────────────────────────── Drivers ────────────────────────────────────

CREATE TABLE IF NOT EXISTS drivers (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    initials            TEXT NOT NULL,
    external            BOOLEAN NOT NULL DEFAULT FALSE,
    transporter         TEXT,
    role                TEXT NOT NULL,
    phone               TEXT NOT NULL,
    email               TEXT,
    permit_no           TEXT NOT NULL,
    permit_class        TEXT NOT NULL,
    permit_expiry       DATE NOT NULL,
    first_aid           BOOLEAN NOT NULL DEFAULT FALSE,
    first_aid_expiry    DATE,
    defensive           BOOLEAN NOT NULL DEFAULT FALSE,
    defensive_expiry    DATE,
    medical_expiry      DATE,
    years_exp           INTEGER NOT NULL DEFAULT 0,
    vehicle_id          TEXT,
    current_assignment  TEXT,
    home_region         TEXT NOT NULL,
    rating              NUMERIC(5,2) NOT NULL DEFAULT 0,
    safety_score        NUMERIC(5,2) NOT NULL DEFAULT 0,
    trip_count          INTEGER,
    violation_count     INTEGER,
    status              TEXT NOT NULL,        -- on-duty | off-duty | rest | external
    notes               TEXT
);

CREATE INDEX IF NOT EXISTS drivers_status_idx       ON drivers (status);
CREATE INDEX IF NOT EXISTS drivers_external_idx     ON drivers (external);

-- ─────────────────────────────── Vehicles ───────────────────────────────────

CREATE TABLE IF NOT EXISTS vehicles (
    id              TEXT PRIMARY KEY,
    plate           TEXT NOT NULL UNIQUE,
    type            TEXT NOT NULL,
    make            TEXT NOT NULL,
    model           TEXT NOT NULL,
    year            INTEGER NOT NULL,
    vehicle_class   TEXT NOT NULL,
    ownership       TEXT NOT NULL,           -- Owned | Hired | MOW
    driver_id       TEXT,
    status          TEXT NOT NULL,           -- moving | idle | maintenance | offline
    location        TEXT NOT NULL,
    lat             DOUBLE PRECISION NOT NULL,
    lng             DOUBLE PRECISION NOT NULL,
    heading         DOUBLE PRECISION NOT NULL DEFAULT 0,
    fuel            DOUBLE PRECISION NOT NULL DEFAULT 0,
    odo             DOUBLE PRECISION NOT NULL DEFAULT 0,
    capacity        TEXT NOT NULL,
    cargo           TEXT,
    last_seen       TIMESTAMPTZ NOT NULL,
    telematics      TEXT,
    fuel_tracker    BOOLEAN NOT NULL DEFAULT FALSE,
    dashcam         BOOLEAN,
    next_service_km DOUBLE PRECISION NOT NULL DEFAULT 0,
    speed           DOUBLE PRECISION NOT NULL DEFAULT 0,
    engine_hours    DOUBLE PRECISION,
    purpose         TEXT,
    mech_status     TEXT NOT NULL,           -- operational | out-of-service | grounded
    alert           TEXT,
    tank_capacity_litres INTEGER             -- NULL = unknown; required for % → litres conversion in fuel analytics
);

CREATE INDEX IF NOT EXISTS vehicles_status_idx      ON vehicles (status);
CREATE INDEX IF NOT EXISTS vehicles_driver_id_idx   ON vehicles (driver_id);
CREATE INDEX IF NOT EXISTS vehicles_mech_status_idx ON vehicles (mech_status);

-- ───────────────────────────────── JMPs ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS jmps (
    id                  TEXT PRIMARY KEY,
    vehicle_id          TEXT NOT NULL,
    driver_id           TEXT NOT NULL,
    purpose             TEXT NOT NULL,
    cargo_description   TEXT NOT NULL DEFAULT '',
    start_date          DATE NOT NULL,
    expected_arrival    DATE NOT NULL,
    designated_parking  TEXT NOT NULL DEFAULT '',
    route_summary       TEXT NOT NULL DEFAULT '',
    route_detail        TEXT NOT NULL DEFAULT '',
    expected_days       INTEGER NOT NULL DEFAULT 1,
    expected_return     DATE NOT NULL,
    distance_km         DOUBLE PRECISION NOT NULL DEFAULT 0,
    fuel_estimate_l     DOUBLE PRECISION NOT NULL DEFAULT 0,
    mileage_request     DOUBLE PRECISION NOT NULL DEFAULT 0,
    mileage_status      TEXT NOT NULL,       -- Pending | Submitted | Approved | Rejected | Disbursed
    total_budget_ugx    DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- toolbox: { completed, completedAt, facilitator, items: { ... 8 booleans ... } }
    toolbox             JSONB NOT NULL DEFAULT '{"completed":false,"items":{}}'::jsonb,
    fatigue_plan        TEXT NOT NULL DEFAULT '',
    incident_contacts   TEXT NOT NULL DEFAULT '',
    convoy_partner      TEXT NOT NULL DEFAULT 'Solo',
    status              TEXT NOT NULL,       -- draft | pending-toolbox | active | completed | cancelled
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by          TEXT NOT NULL,
    approved_by         TEXT,
    approved_at         TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    parking_photos      TEXT[] NOT NULL DEFAULT '{}',
    source_request_id   TEXT
);

CREATE INDEX IF NOT EXISTS jmps_vehicle_id_idx ON jmps (vehicle_id);
CREATE INDEX IF NOT EXISTS jmps_driver_id_idx  ON jmps (driver_id);
CREATE INDEX IF NOT EXISTS jmps_status_idx     ON jmps (status);
CREATE INDEX IF NOT EXISTS jmps_start_date_idx ON jmps (start_date);

-- ──────────────────────────────── Cargo ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS cargo (
    id                   TEXT PRIMARY KEY,
    convoy               TEXT NOT NULL,
    truck_plate          TEXT NOT NULL,
    driver_name          TEXT NOT NULL,
    driver_phone         TEXT NOT NULL,
    transporter          TEXT NOT NULL,
    cargo_nature         TEXT NOT NULL,
    container            TEXT NOT NULL,
    departure_mombasa    DATE,
    departure_malaba     DATE,
    departure_kampala    DATE,
    arrival_acp          DATE,
    offloading_date      DATE,
    stage                TEXT NOT NULL,
    urgency              TEXT NOT NULL,      -- normal | high | low
    demobilised          BOOLEAN,
    demobilised_at       TIMESTAMPTZ,
    remarks              TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- stage_history is an append-only event log: [{stage, at, by?, note?}]
    stage_history        JSONB NOT NULL DEFAULT '[]'::jsonb
);

CREATE INDEX IF NOT EXISTS cargo_stage_idx       ON cargo (stage);
CREATE INDEX IF NOT EXISTS cargo_truck_plate_idx ON cargo (truck_plate);

-- ────────────────────────────── Cargo Docs ──────────────────────────────────

CREATE TABLE IF NOT EXISTS cargo_docs (
    id        TEXT PRIMARY KEY,
    cargo_id  TEXT NOT NULL,
    doc_type  TEXT NOT NULL,
    doc_no    TEXT,
    issued    DATE,
    expiry    DATE,
    issuer    TEXT,
    notes     TEXT
);

CREATE INDEX IF NOT EXISTS cargo_docs_cargo_id_idx ON cargo_docs (cargo_id);

-- ──────────────────────────── Fuel Records ──────────────────────────────────

CREATE TABLE IF NOT EXISTS fuel_records (
    id              TEXT PRIMARY KEY,
    vehicle_id      TEXT NOT NULL,
    driver_id       TEXT,
    date            DATE NOT NULL,
    litres          DOUBLE PRECISION NOT NULL,
    unit_price      DOUBLE PRECISION NOT NULL,
    total           DOUBLE PRECISION NOT NULL,
    station         TEXT NOT NULL,
    invoice         TEXT,
    odo             DOUBLE PRECISION NOT NULL DEFAULT 0,
    notes           TEXT,
    anomaly         BOOLEAN,
    anomaly_reason  TEXT
);

CREATE INDEX IF NOT EXISTS fuel_records_vehicle_id_idx ON fuel_records (vehicle_id);
CREATE INDEX IF NOT EXISTS fuel_records_date_idx       ON fuel_records (date);
CREATE INDEX IF NOT EXISTS fuel_records_anomaly_idx    ON fuel_records (anomaly) WHERE anomaly = TRUE;

-- ────────────────────────────── Maintenance ─────────────────────────────────

CREATE TABLE IF NOT EXISTS maintenance_items (
    id            TEXT PRIMARY KEY,
    vehicle_id    TEXT NOT NULL,
    date          DATE NOT NULL,
    type          TEXT NOT NULL,            -- Service | Repair | Inspection | Tyres | Brakes | Engine
    service       TEXT NOT NULL,
    status        TEXT NOT NULL,            -- scheduled | in-progress | completed | overdue
    priority      TEXT NOT NULL,            -- low | normal | high | critical
    workshop      TEXT NOT NULL,
    odo           DOUBLE PRECISION NOT NULL DEFAULT 0,
    next_due_km   DOUBLE PRECISION,
    cost          DOUBLE PRECISION NOT NULL DEFAULT 0,
    parts         TEXT,
    notes         TEXT
);

CREATE INDEX IF NOT EXISTS maintenance_vehicle_id_idx ON maintenance_items (vehicle_id);
CREATE INDEX IF NOT EXISTS maintenance_status_idx     ON maintenance_items (status);

-- ──────────────────────────────── Parts ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS parts (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    category        TEXT NOT NULL,
    sku             TEXT NOT NULL UNIQUE,
    stock           INTEGER NOT NULL DEFAULT 0,
    reorder_point   INTEGER NOT NULL DEFAULT 0,
    unit_cost       DOUBLE PRECISION NOT NULL DEFAULT 0,
    location        TEXT,
    vendor          TEXT,
    notes           TEXT
);

-- ──────────────────────────────── Tyres ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS tyres (
    id                TEXT PRIMARY KEY,
    vehicle_id        TEXT NOT NULL,
    position          TEXT NOT NULL,
    brand             TEXT NOT NULL,
    model             TEXT NOT NULL,
    serial            TEXT NOT NULL,
    mounted_date      DATE NOT NULL,
    mounted_km        DOUBLE PRECISION NOT NULL DEFAULT 0,
    tread_depth_mm    DOUBLE PRECISION NOT NULL,
    tread_initial_mm  DOUBLE PRECISION NOT NULL,
    status            TEXT NOT NULL          -- good | replace-soon | replace-now
);

CREATE INDEX IF NOT EXISTS tyres_vehicle_id_idx ON tyres (vehicle_id);

-- ──────────────────────────────── Trips ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS trips (
    id              TEXT PRIMARY KEY,
    driver_id       TEXT NOT NULL,
    vehicle_id      TEXT NOT NULL,
    date            DATE NOT NULL,
    start_location  TEXT NOT NULL,
    end_location    TEXT NOT NULL,
    distance_km     DOUBLE PRECISION NOT NULL,
    duration_min    DOUBLE PRECISION NOT NULL,
    fuel_l          DOUBLE PRECISION NOT NULL,
    status          TEXT NOT NULL,            -- completed | cancelled | in-progress
    rating          DOUBLE PRECISION,
    notes           TEXT
);

CREATE INDEX IF NOT EXISTS trips_driver_id_idx  ON trips (driver_id);
CREATE INDEX IF NOT EXISTS trips_vehicle_id_idx ON trips (vehicle_id);
CREATE INDEX IF NOT EXISTS trips_date_idx       ON trips (date);

-- ─────────────────────────── Safety Events ─────────────────────────────────

CREATE TABLE IF NOT EXISTS safety_events (
    id           TEXT PRIMARY KEY,
    vehicle_id   TEXT NOT NULL,
    driver_id    TEXT,
    date         TIMESTAMPTZ NOT NULL,
    type         TEXT NOT NULL,
    severity     TEXT NOT NULL,             -- info | warn | crit
    status       TEXT NOT NULL,             -- open | investigating | resolved | closed
    location     TEXT,
    description  TEXT NOT NULL,
    action       TEXT,
    reported_by  TEXT
);

CREATE INDEX IF NOT EXISTS safety_status_idx   ON safety_events (status);
CREATE INDEX IF NOT EXISTS safety_severity_idx ON safety_events (severity);
CREATE INDEX IF NOT EXISTS safety_date_idx     ON safety_events (date DESC);

-- ────────────────────────── Compliance Items ───────────────────────────────

CREATE TABLE IF NOT EXISTS compliance_items (
    id          TEXT PRIMARY KEY,
    vehicle_id  TEXT,
    driver_id   TEXT,
    doc_type    TEXT NOT NULL,
    doc_number  TEXT,
    issuer      TEXT,
    issued      DATE,
    expiry      DATE,                       -- nullable: missing-status items have no expiry
    status      TEXT NOT NULL,              -- valid | expiring | expired | missing
    notes       TEXT,
    CHECK (vehicle_id IS NOT NULL OR driver_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS compliance_status_idx     ON compliance_items (status);
CREATE INDEX IF NOT EXISTS compliance_driver_id_idx  ON compliance_items (driver_id);
CREATE INDEX IF NOT EXISTS compliance_vehicle_id_idx ON compliance_items (vehicle_id);
CREATE INDEX IF NOT EXISTS compliance_expiry_idx     ON compliance_items (expiry);

-- ─────────────────────────── Service Requests ──────────────────────────────

CREATE TABLE IF NOT EXISTS service_requests (
    id                      TEXT PRIMARY KEY,
    requester_name          TEXT NOT NULL,
    requester_dept          TEXT NOT NULL,
    requester_phone         TEXT,
    purpose                 TEXT NOT NULL,
    destination             TEXT NOT NULL,
    start_date              DATE NOT NULL,
    end_date                DATE,
    pax                     INTEGER,
    cargo_type              TEXT,
    urgency                 TEXT NOT NULL,        -- low | normal | high | critical
    preferred_vehicle_type  TEXT,
    reviewer_notes          TEXT,
    status                  TEXT NOT NULL,        -- submitted | reviewed | approved | assigned | rejected | completed
    submitted_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by              TEXT,
    assigned_vehicle_id     TEXT,
    assigned_driver_id      TEXT,
    jmp_id                  TEXT,
    task_id                 TEXT
);

CREATE INDEX IF NOT EXISTS service_requests_status_idx       ON service_requests (status);
CREATE INDEX IF NOT EXISTS service_requests_submitted_at_idx ON service_requests (submitted_at DESC);

-- ──────────────────────────────── Tasks ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS task_items (
    id             TEXT PRIMARY KEY,
    title          TEXT NOT NULL,
    state          TEXT NOT NULL,             -- open | in-review | in-progress | done
    priority       TEXT NOT NULL,             -- low | normal | high | critical
    assignee_name  TEXT NOT NULL,
    due_date       DATE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at   TIMESTAMPTZ,
    source         TEXT NOT NULL,             -- service-request | manual | system
    source_id      TEXT,
    -- links: array of { type: "request" | "jmp" | "cargo", id: string }
    links          JSONB NOT NULL DEFAULT '[]'::jsonb
);

CREATE INDEX IF NOT EXISTS tasks_state_idx     ON task_items (state);
CREATE INDEX IF NOT EXISTS tasks_source_id_idx ON task_items (source_id);

-- ─────────────────────────── Deployment Days ───────────────────────────────

-- Entries are denormalized into a JSONB column instead of a separate
-- normalized table; this matches how cargo.stage_history and task.links
-- are stored, lets the API serve a single round-trip per day, and keeps
-- the generic CRUD store from needing a special-case for this entity.
-- Cross-day queries on individual entries can use JSONB containment
-- (`entries @> '[{"vehicleId":"V01"}]'`) when needed.
CREATE TABLE IF NOT EXISTS deployment_days (
    id           TEXT PRIMARY KEY,
    date         DATE NOT NULL,
    compiled_by  TEXT NOT NULL,
    notes        TEXT NOT NULL DEFAULT '',
    entries      JSONB NOT NULL DEFAULT '[]'::jsonb,
    UNIQUE (date)
);

-- ──────────────────────────── Operator Ticker ──────────────────────────────

-- Single-row table; the singleton constraint is enforced via a fixed PK value.
CREATE TABLE IF NOT EXISTS operator_ticker (
    id        TEXT PRIMARY KEY DEFAULT 'singleton' CHECK (id = 'singleton'),
    diesel    DOUBLE PRECISION NOT NULL,
    ugx       DOUBLE PRECISION NOT NULL,
    operator  TEXT NOT NULL,
    role      TEXT NOT NULL
);

-- ───────────────── RBAC: users, groups, permissions, sessions ──────────────
--
-- Models Django's auth.User / auth.Group / auth.Permission. A user has many
-- groups; a group has many permissions; a user can also have permissions
-- directly. has_perm(user, codename) returns true if:
--   user.is_superuser OR
--   user has the perm directly OR
--   any of user's groups has the perm.

CREATE TABLE IF NOT EXISTS users (
    id              BIGSERIAL PRIMARY KEY,
    username        TEXT NOT NULL UNIQUE,
    email           TEXT,
    full_name       TEXT NOT NULL DEFAULT '',
    password_hash   TEXT NOT NULL,
    email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    is_staff        BOOLEAN NOT NULL DEFAULT FALSE,   -- can access admin endpoints
    is_superuser    BOOLEAN NOT NULL DEFAULT FALSE,   -- bypasses all permission checks
    date_joined     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS users_email_idx ON users (email);

-- Single table for email-driven token flows. purpose distinguishes
-- password-reset from email-verification. Tokens are stored as SHA-256
-- hex digests, never plaintext; the plaintext travels in the email link
-- only. Tokens are single-use (used_at != NULL) and short-lived.
CREATE TABLE IF NOT EXISTS auth_tokens (
    id          BIGSERIAL PRIMARY KEY,
    token_hash  TEXT NOT NULL UNIQUE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose     TEXT NOT NULL CHECK (purpose IN ('reset_password', 'verify_email')),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS auth_tokens_user_purpose_idx ON auth_tokens (user_id, purpose);
CREATE INDEX IF NOT EXISTS auth_tokens_expires_at_idx   ON auth_tokens (expires_at);

CREATE TABLE IF NOT EXISTS auth_groups (
    id   BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS auth_permissions (
    id        BIGSERIAL PRIMARY KEY,
    codename  TEXT NOT NULL UNIQUE,                -- e.g. add_vehicle, approve_mileage_jmp
    name      TEXT NOT NULL,                       -- human-readable label
    entity    TEXT NOT NULL                        -- which entity this perm gates (vehicle, jmp, ...)
);

CREATE INDEX IF NOT EXISTS auth_permissions_entity_idx ON auth_permissions (entity);

CREATE TABLE IF NOT EXISTS user_groups (
    user_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES auth_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);

CREATE TABLE IF NOT EXISTS group_permissions (
    group_id      BIGINT NOT NULL REFERENCES auth_groups(id) ON DELETE CASCADE,
    permission_id BIGINT NOT NULL REFERENCES auth_permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, permission_id)
);

CREATE TABLE IF NOT EXISTS user_user_permissions (
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permission_id BIGINT NOT NULL REFERENCES auth_permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, permission_id)
);

-- Sessions are stored server-side. session_key is the random opaque string
-- delivered to the client as an HttpOnly cookie. Expired rows are pruned by
-- the auth middleware on access.
CREATE TABLE IF NOT EXISTS auth_sessions (
    session_key  TEXT PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip           TEXT,
    user_agent   TEXT
);

CREATE INDEX IF NOT EXISTS auth_sessions_user_id_idx    ON auth_sessions (user_id);
CREATE INDEX IF NOT EXISTS auth_sessions_expires_at_idx ON auth_sessions (expires_at);

-- ──────────────────────────── Audit Entries ────────────────────────────────
--
-- user_id links to the authenticated principal; "user" is a denormalized
-- username snapshot at write-time so logs survive user deletion / rename.

CREATE TABLE IF NOT EXISTS audit_entries (
    id      BIGSERIAL PRIMARY KEY,
    ts      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    action  TEXT NOT NULL,
    entity  TEXT NOT NULL,
    ref_id  TEXT NOT NULL,
    details TEXT,
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    "user"  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS audit_ts_idx     ON audit_entries (ts DESC);
CREATE INDEX IF NOT EXISTS audit_entity_idx ON audit_entries (entity, ref_id);
CREATE INDEX IF NOT EXISTS audit_user_idx   ON audit_entries (user_id);

-- ─────────────────── IoT: devices, pings, daily summary ───────────────────
--
-- iot_devices is the registry of physical units (Teltonika FMC650 / FMB920 /
-- FMC130 etc). Each device authenticates one of two ways:
--   - HTTP ingestion: shared API key (sha256 stored as api_key_hash).
--   - Codec 8 TCP: by IMEI/serial only — Teltonika protocol has no separate
--     credential, so the device is trusted iff its serial matches a row
--     here with is_active = TRUE.
--
-- A device is bound to one vehicle at a time via vehicle_id; rotating a
-- unit between vehicles is just a column update.

CREATE TABLE IF NOT EXISTS iot_devices (
    id            BIGSERIAL PRIMARY KEY,
    serial        TEXT NOT NULL UNIQUE,                 -- IMEI / hardware serial
    label         TEXT,
    vehicle_id    TEXT REFERENCES vehicles(id) ON DELETE SET NULL,
    api_key_hash  TEXT,                                 -- sha256 hex of HTTP key, NULL = TCP-only
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen     TIMESTAMPTZ,
    last_ip       TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS iot_devices_vehicle_idx ON iot_devices (vehicle_id);

-- Raw position pings. Volume target: ~50 vehicles × 1 ping/min ≈ 26M rows/yr,
-- retained 365 days by cmd/telemetry-purge. The composite (vehicle_id, ts)
-- btree serves track-replay queries; the BRIN(ts) index is cheap insurance
-- for time-range scans across the whole fleet (purge, fleet-wide reports).
-- raw JSONB holds the full IO map from the device for forensic queries.
CREATE TABLE IF NOT EXISTS telemetry_pings (
    id          BIGSERIAL PRIMARY KEY,
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
    fuel_level  DOUBLE PRECISION,                       -- 0-100% or litres; per-device interpretation
    ignition    BOOLEAN,
    event_id    INTEGER,                                -- device-reported event code (Codec 8 IO ID)
    raw         JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS telemetry_pings_vehicle_ts_idx ON telemetry_pings (vehicle_id, ts DESC);
CREATE INDEX IF NOT EXISTS telemetry_pings_ts_brin_idx    ON telemetry_pings USING BRIN (ts);

-- Daily summary lives forever even after raw pings are purged. Populated by
-- cmd/telemetry-aggregate.
CREATE TABLE IF NOT EXISTS telemetry_daily (
    vehicle_id        TEXT NOT NULL,
    day               DATE NOT NULL,
    ping_count        INTEGER NOT NULL DEFAULT 0,
    distance_km       DOUBLE PRECISION NOT NULL DEFAULT 0,
    max_speed_kmh     DOUBLE PRECISION,
    avg_speed_kmh     DOUBLE PRECISION,
    fuel_used_litres  DOUBLE PRECISION,
    moving_minutes    INTEGER NOT NULL DEFAULT 0,
    idle_minutes      INTEGER NOT NULL DEFAULT 0,
    first_ping        TIMESTAMPTZ,
    last_ping         TIMESTAMPTZ,
    PRIMARY KEY (vehicle_id, day)
);

CREATE INDEX IF NOT EXISTS telemetry_daily_day_idx ON telemetry_daily (day DESC);

-- Auto-detected fuel events from the telemetry stream. Distinct from
-- fuel_records (manual refuel ledger) so we don't conflate human entries
-- with pattern-detected ones; a future job can match-and-link the two.
--
--   kind = 'refuel' → positive jump in fuel_level (tank filled)
--   kind = 'drop'   → negative jump (consumption surge or theft/leak)
--
-- delta_pct is signed (positive for refuel, negative for drop).
-- delta_litres is set when the vehicle has a known tank_capacity_litres.
CREATE TABLE IF NOT EXISTS fuel_events (
    id              BIGSERIAL PRIMARY KEY,
    vehicle_id      TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('refuel', 'drop')),
    ts              TIMESTAMPTZ NOT NULL,
    delta_pct       DOUBLE PRECISION NOT NULL,
    delta_litres    DOUBLE PRECISION,
    before_pct      DOUBLE PRECISION NOT NULL,
    after_pct       DOUBLE PRECISION NOT NULL,
    odo             DOUBLE PRECISION,
    speed_kmh       DOUBLE PRECISION,
    ignition        BOOLEAN,
    confidence      TEXT NOT NULL DEFAULT 'high',  -- high | medium | low
    notes           TEXT,
    UNIQUE (vehicle_id, ts, kind)
);

CREATE INDEX IF NOT EXISTS fuel_events_vehicle_ts_idx ON fuel_events (vehicle_id, ts DESC);
CREATE INDEX IF NOT EXISTS fuel_events_kind_idx       ON fuel_events (kind);

