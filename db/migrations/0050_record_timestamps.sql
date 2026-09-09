-- 0050: created_at / updated_at on every domain table
--
-- ── What this fixes ────────────────────────────────────────────────────────
-- No fleet model carried updatedAt and 15 of 19 tables had no created_at, so
-- every record the API returned had both fields empty. The Next.js client uses
-- them for two things, and both were silently broken:
--
--   Optimistic concurrency. Its collection revision is `${rowCount}:${max
--   updatedAt}`, which with every stamp empty collapses to `${rowCount}:0`. Two
--   people editing different rows of the same list therefore computed the same
--   revision, neither PUT conflicted, and the second writer silently reverted
--   the first one's row to their stale copy.
--
--   Ordering. Tables page at ten rows and sort newest-first by these stamps.
--   With all of them zero the sort fell through to the id tie-break, so a
--   just-saved record landed on an arbitrary page and read as lost.
--
-- ── Why a trigger and not just a DEFAULT ───────────────────────────────────
-- The store layer derives its UPDATE statement reflectively from the model's
-- `db` tags and names EVERY non-id column, so Collection.Replace writes back
-- whatever the Go struct happens to hold — which for updated_at would be the
-- value read at the start of the request, i.e. never moving. A BEFORE trigger
-- fires after the statement's SET list is applied and wins over it, which makes
-- the column authoritative no matter what any caller sends.
--
-- The same mechanism protects created_at, which a PUT would otherwise be able to
-- null: a full-replace body that omits it binds the empty string, and '' cast to
-- timestamptz writes NULL. On UPDATE the trigger restores the stored value, so
-- created_at is immutable by construction rather than by convention.
--
-- ── Backfill ───────────────────────────────────────────────────────────────
-- Existing rows take NOW() from the DEFAULT, so every pre-existing record shares
-- one timestamp. That is accepted: the stamps are monotonic from this migration
-- forward, which is all the revision check and the sort need. The four tables
-- that already had created_at (jmps, cargo, task_items, inspection_templates)
-- keep their real values — ADD COLUMN IF NOT EXISTS is a no-op there.

CREATE OR REPLACE FUNCTION touch_row() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        -- COALESCE rather than an unconditional NOW(): an import or a backfill
        -- supplying real historical stamps must keep them.
        NEW.created_at := COALESCE(NEW.created_at, NOW());
        NEW.updated_at := COALESCE(NEW.updated_at, NOW());
    ELSE
        NEW.created_at := OLD.created_at;
        NEW.updated_at := NOW();
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE
    t TEXT;
    domain_tables TEXT[] := ARRAY[
        'vehicles', 'drivers', 'jmps', 'cargo', 'cargo_docs',
        'fuel_records', 'fuel_requests', 'maintenance_items',
        'parts', 'tyres', 'trips', 'safety_events', 'compliance_items',
        'service_requests', 'task_items', 'deployment_days',
        'inspection_templates', 'vehicle_inspections', 'pm_schedules',
        -- The authorisation matrix from 0049 gets the same treatment, so the
        -- three new collections behave like every other one from day one.
        'vehicle_categories', 'permit_classes', 'permit_authorisations'
    ];
BEGIN
    FOREACH t IN ARRAY domain_tables LOOP
        EXECUTE format(
            'ALTER TABLE %I ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()', t);
        EXECUTE format(
            'ALTER TABLE %I ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()', t);
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', t || '_touch', t);
        EXECUTE format(
            'CREATE TRIGGER %I BEFORE INSERT OR UPDATE ON %I '
            'FOR EACH ROW EXECUTE FUNCTION touch_row()', t || '_touch', t);
        -- The client sorts newest-first on every list, and the revision check
        -- reads max(updated_at) on every save.
        EXECUTE format(
            'CREATE INDEX IF NOT EXISTS %I ON %I (updated_at DESC)', t || '_updated_at_idx', t);
    END LOOP;
END;
$$;
