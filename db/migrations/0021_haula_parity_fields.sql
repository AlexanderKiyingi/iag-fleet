-- Close HAULA frontend feature-parity gaps. The reflective store derives columns
-- from db:"..." struct tags, so these columns must exist for the new model
-- fields to round-trip. Idempotent (ADD COLUMN IF NOT EXISTS) and self-heal-safe.

-- SafetyEvent incident detail the UI already captures (GPS pin, injuries, cost,
-- link to the work order it spawned, authorities involved).
ALTER TABLE safety_events
    ADD COLUMN IF NOT EXISTS gps_lat      DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS gps_lng      DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS injuries     INTEGER,
    ADD COLUMN IF NOT EXISTS cost         DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS linked_wo_id TEXT,
    ADD COLUMN IF NOT EXISTS authorities  TEXT;

-- Work-order fields the UI carries: assigned mechanic and the incident that
-- spawned the WO (the other half of safety_events.linked_wo_id).
ALTER TABLE maintenance_items
    ADD COLUMN IF NOT EXISTS mechanic         TEXT,
    ADD COLUMN IF NOT EXISTS linked_safety_id TEXT;

CREATE INDEX IF NOT EXISTS maintenance_linked_safety_idx
    ON maintenance_items (linked_safety_id)
    WHERE linked_safety_id IS NOT NULL;
