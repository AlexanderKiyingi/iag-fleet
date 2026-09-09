-- 0049: the driver–vehicle authorisation matrix (PRD FR-DRV-04)
--
-- Which licence class may operate which kind of vehicle is a licensing question
-- whose answer differs by country and by operator, so it is configured rather
-- than compiled: an operator defines the categories, the classes, and the pairs
-- that are authorised.
--
-- The frontend has shipped three complete read-write screens for this against
-- /api/vehicle-categories, /api/permit-classes and /api/permit-authorisations —
-- endpoints that did not exist on any branch of this service, so all three tabs
-- 404'd. This is the missing half.
--
-- FAIL OPEN. An empty matrix means "no opinion", never "deny". Every existing
-- deployment has an empty matrix, so a fail-closed check would ground the entire
-- fleet the moment this deploys. The rule only starts biting once somebody has
-- configured at least one authorisation — see validateDriverDispatch.

CREATE TABLE IF NOT EXISTS vehicle_categories (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    -- The operator's own short code ("HGV", "PSV-M"). Unique because it is what
    -- a permit authorisation is read against by a human checking the matrix.
    code        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    active      BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS permit_classes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- As the licensing authority writes it: "CE", "D1", "B". Matched against
    -- drivers.permit_class, which is free text and always has been.
    code        TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    active      BOOLEAN NOT NULL DEFAULT TRUE
);

-- One cell of the matrix: this class may operate this category. Each row is an
-- operator asserting an authorisation, so there is nothing to default and
-- nothing to infer — a pair that is not here is not authorised.
CREATE TABLE IF NOT EXISTS permit_authorisations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    permit_class_id UUID NOT NULL REFERENCES permit_classes(id)     ON DELETE CASCADE,
    category_id     UUID NOT NULL REFERENCES vehicle_categories(id) ON DELETE CASCADE,
    notes           TEXT NOT NULL DEFAULT '',
    UNIQUE (permit_class_id, category_id)
);

CREATE INDEX IF NOT EXISTS permit_authorisations_class_idx    ON permit_authorisations (permit_class_id);
CREATE INDEX IF NOT EXISTS permit_authorisations_category_idx ON permit_authorisations (category_id);

-- The classification the matrix is read against. Separate from vehicles.type,
-- which is free text and in practice carries brand names ("KOMATSU"), and from
-- vehicle_class. Nullable: an unclassified vehicle has no matrix opinion, which
-- is the fail-open case again.
--
-- ON DELETE SET NULL rather than RESTRICT: retiring a category should not be
-- blocked by the fleet still referencing it, and a vehicle with no category is
-- a state the rule already handles.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS category_id UUID
    REFERENCES vehicle_categories(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS vehicles_category_id_idx ON vehicles (category_id);
