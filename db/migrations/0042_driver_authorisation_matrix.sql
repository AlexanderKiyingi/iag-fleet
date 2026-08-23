-- PRD FR-DRV-04: a driver may only be assigned to vehicle categories they are
-- licensed for.
--
-- The taxonomy is deliberately NOT hardcoded. Which permit class may operate a
-- grader is a licensing question, and the answer differs by country and by
-- operator; a list compiled here would be a guess with legal consequences, and
-- re-tagging a fleet twice is worse than configuring it once. So categories,
-- permit classes and the mapping between them are all records a user creates
-- and maintains, and this migration ships the mechanism with no content.
--
-- Three tables rather than two columns, because the mapping is many-to-many: a
-- class authorises several categories, and a category is reachable by several
-- classes.

CREATE TABLE IF NOT EXISTS vehicle_categories (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    code        TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    -- Retiring a category must not rewrite the vehicles already tagged with it,
    -- so it is deactivated rather than deleted.
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS vehicle_categories_name_idx
    ON vehicle_categories (lower(name));

CREATE TABLE IF NOT EXISTS permit_classes (
    id          TEXT PRIMARY KEY,
    -- The letter or code as the licensing authority writes it.
    code        TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS permit_classes_code_idx
    ON permit_classes (lower(code));

-- The matrix itself. One row = "this class may operate this category".
CREATE TABLE IF NOT EXISTS permit_class_authorisations (
    id          TEXT PRIMARY KEY,
    permit_class_id TEXT NOT NULL REFERENCES permit_classes (id) ON DELETE CASCADE,
    category_id     TEXT NOT NULL REFERENCES vehicle_categories (id) ON DELETE CASCADE,
    notes       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The same pair twice would mean nothing and would make "is this authorised"
-- ambiguous to read.
CREATE UNIQUE INDEX IF NOT EXISTS permit_class_authorisations_pair_idx
    ON permit_class_authorisations (permit_class_id, category_id);

CREATE INDEX IF NOT EXISTS permit_class_authorisations_class_idx
    ON permit_class_authorisations (permit_class_id);

-- A vehicle's category, separate from `type`. `type` is free text carrying
-- brand names in production -- SINO TRUCK, SANY CRANE, DRONE(DIESEL), FUSO,
-- and `truck` in lowercase -- 17 distinct values over 38 vehicles. It is useful
-- to a person reading a row and useless to a rule, so the rule gets its own
-- column and `type` is left alone.
ALTER TABLE vehicles
    ADD COLUMN IF NOT EXISTS category_id TEXT REFERENCES vehicle_categories (id);

CREATE INDEX IF NOT EXISTS vehicles_category_idx ON vehicles (category_id);

-- No seed rows on purpose.
--
-- An empty matrix means "nothing is configured", and the check that reads it
-- fails OPEN in that state. Seeding a plausible-looking taxonomy would turn a
-- fleet-wide block on the day it deployed -- which is exactly the failure the
-- 2000-01-01 permit placeholder already caused, where all 20 drivers became
-- un-dispatchable and nothing said so.
