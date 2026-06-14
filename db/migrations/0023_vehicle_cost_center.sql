-- Per-vehicle cost center. When fleet issues spare parts to iag-warehouse on a
-- vehicle's maintenance work order, this lets finance cost the issue to the
-- vehicle's bucket (department, fleet line, project) instead of a single
-- blanket "fleet-maintenance" department. Optional — NULL/empty falls back to
-- the configured issue department.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS cost_center TEXT;
