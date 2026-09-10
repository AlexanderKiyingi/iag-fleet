-- 0048: vehicle asset lifecycle and disposal (PRD FR-VEH-06)
--
-- The frontend has carried this feature in full for some time: a state machine
-- (src/lib/iag/vehicle-lifecycle.ts), a modal, a transition route that reads the
-- current state, refuses invalid moves and stamps attribution from the verified
-- session. It PATCHes eight fields onto /api/vehicles/:id.
--
-- models.Vehicle had a place for none of them, and the generic PATCH merges into
-- the struct, so Go's json.Unmarshal dropped every one of them and answered 200
-- with the unchanged row. The API reported "Vehicle moved from Active to
-- Grounded" and the next read said Active. This migration and the matching
-- struct fields are what make that feature real.
--
-- Deliberately NOT `status`: that column is live operational state written by
-- telemetry and read by dispatch validation. Lifecycle is the asset's own
-- disposition, which is why it is a separate axis.

ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS lifecycle_state   TEXT NOT NULL DEFAULT 'Active';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS lifecycle_reason  TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS lifecycle_at      TIMESTAMPTZ;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS lifecycle_by      TEXT NOT NULL DEFAULT '';

-- Disposal detail. Only meaningful once lifecycle_state = 'Disposed', and left
-- nullable rather than defaulted so "not disposed" and "disposed for nothing"
-- stay distinguishable — proceeds of 0 is a real outcome (scrapped, donated).
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS disposal_method   TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS disposal_date     DATE;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS disposal_proceeds DOUBLE PRECISION;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS disposal_buyer    TEXT NOT NULL DEFAULT '';

-- The fleet register filters on disposition constantly ("show me what is still
-- operable"), and a disposed vehicle must drop out of every dispatch picker.
CREATE INDEX IF NOT EXISTS vehicles_lifecycle_state_idx ON vehicles (lifecycle_state);
