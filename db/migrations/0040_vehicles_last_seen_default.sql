-- vehicles.last_seen is TIMESTAMPTZ NOT NULL with no default, and nothing on
-- the write path fills it. Creating a vehicle through the API therefore failed
-- on a raw null-violation: "null value in column last_seen of relation
-- vehicles violates not-null constraint". Not a validation message a caller
-- could act on, and no vehicle form knows the answer anyway -- last_seen is
-- telemetry state, meaning when the vehicle last reported.
--
-- Every other table in this schema that carries the same idea already defaults
-- it (see the telemetry tables in 0001). This brings vehicles in line, so a
-- vehicle created before it has ever reported reads as "last seen when it was
-- registered" rather than being impossible to create.
--
-- The store's dbdefault mechanism omits a zero-valued column from the INSERT so
-- the default can apply; that mechanism needs a default to exist, which is what
-- this adds. Columns that already had one (service_requests.submitted_at,
-- task_items.created_at, cargo.created_at, jmps.created_at,
-- jmps.parking_photos, inspection_templates.created_at) needed only the tag.

ALTER TABLE vehicles ALTER COLUMN last_seen SET DEFAULT NOW();
