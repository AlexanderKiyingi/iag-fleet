-- PRD FR-VEH-06: track an asset through Ordered -> In commissioning -> Active ->
-- Under maintenance -> Grounded -> Held for disposal -> Disposed.
--
-- This is a NEW column rather than a reinterpretation of vehicles.status, and
-- the distinction is load-bearing. `status` is live operational state (moving |
-- idle | maintenance | offline): the telemetry tick writes it, the dashboard,
-- analytics and reports count it, and validateVehicleDispatchable reads it --
-- `offline` and `maintenance` are what stop a vehicle being dispatched.
--
-- Putting the lifecycle there would break both halves. "Held for disposal" is
-- neither `offline` nor `maintenance`, so a vehicle awaiting scrapping would
-- read as dispatchable; and the next telemetry tick would overwrite the
-- lifecycle with `idle` regardless. They are two facts about a vehicle that
-- change for different reasons, so they get two columns.
--
-- mech_status (operational | out-of-service | grounded) is a third, and is
-- about mechanical condition rather than where the asset is in its life.
--
-- Defaults to 'Active': every existing vehicle is in service, and the column is
-- NOT NULL so the state machine never has to reason about an absent value.
-- Disposal detail (FR-VEH-10) rides alongside, so a retirement posted to the
-- ERP can say how the asset went and for how much.

ALTER TABLE vehicles
    ADD COLUMN IF NOT EXISTS lifecycle_state  TEXT NOT NULL DEFAULT 'Active',
    ADD COLUMN IF NOT EXISTS lifecycle_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS lifecycle_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS lifecycle_by     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS disposal_method  TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS disposal_date    DATE,
    ADD COLUMN IF NOT EXISTS disposal_proceeds NUMERIC(14,2),
    ADD COLUMN IF NOT EXISTS disposal_buyer   TEXT NOT NULL DEFAULT '';

-- The state machine lives in the application; the constraint here stops
-- anything writing a state the machine has never heard of -- a backfill, a
-- script, or a future handler.
ALTER TABLE vehicles
    DROP CONSTRAINT IF EXISTS vehicles_lifecycle_state_known;

ALTER TABLE vehicles
    ADD CONSTRAINT vehicles_lifecycle_state_known CHECK (
        lifecycle_state IN (
            'Ordered', 'In commissioning', 'Active', 'Under maintenance',
            'Grounded', 'Held for disposal', 'Disposed'
        )
    );

-- Fleet-wide lifecycle counts are a dashboard question ("how many are held for
-- disposal"), so the column is worth an index.
CREATE INDEX IF NOT EXISTS vehicles_lifecycle_state_idx ON vehicles (lifecycle_state);
