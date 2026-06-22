-- Fuel requests: a pre-authorisation lifecycle for fuel purchases. Distinct
-- from fuel_records (which log already-dispensed fuel). A request is raised,
-- approved or rejected, then fulfilled — fulfilment spawns a fuel_records row
-- and stamps fuel_record_id back here so the two are linked. The existing
-- fleet.fuel.recorded finance event fires from the spawned fuel record, so no
-- extra finance wiring is needed for the spend itself.
CREATE TABLE IF NOT EXISTS fuel_requests (
    id               TEXT PRIMARY KEY,
    vehicle_id       TEXT NOT NULL,
    driver_id        TEXT,
    requester_name   TEXT NOT NULL DEFAULT '',
    requester_dept   TEXT,
    requested_litres DOUBLE PRECISION NOT NULL DEFAULT 0,
    est_unit_price   DOUBLE PRECISION NOT NULL DEFAULT 0,
    est_total        DOUBLE PRECISION NOT NULL DEFAULT 0,
    station          TEXT,
    purpose          TEXT,
    urgency          TEXT,
    -- submitted | approved | rejected | fulfilled | cancelled
    status           TEXT NOT NULL DEFAULT 'submitted',
    notes            TEXT,
    reviewer_notes   TEXT,
    submitted_at     TIMESTAMPTZ,
    approved_by      TEXT,
    approved_at      TIMESTAMPTZ,
    fuel_record_id   TEXT,
    created_by       TEXT
);

CREATE INDEX IF NOT EXISTS fuel_requests_vehicle_id_idx ON fuel_requests (vehicle_id);
CREATE INDEX IF NOT EXISTS fuel_requests_status_idx     ON fuel_requests (status);
CREATE INDEX IF NOT EXISTS fuel_requests_submitted_idx  ON fuel_requests (submitted_at);
