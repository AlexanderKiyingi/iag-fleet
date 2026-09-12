-- 0056: which vehicles a geofence watches, and what a crossing means.
--
-- Both were missing, and the gap showed up the same way: every fence was
-- evaluated for every vehicle, and every crossing meant "log it". A customer
-- site fence therefore raised arrivals for all 37 trucks, and the events for
-- the two that actually serve the site were lost in the noise from the
-- thirty-five that never go there.
--
-- Nothing here changes behaviour on its own. A fence with no rows in
-- geofence_vehicles stays fleet-wide, and rule defaults to 'watch', which is
-- what every fence already did — so the six seeded fences behave identically
-- until somebody edits one.

-- The rule. Deliberately TEXT with a CHECK rather than an enum: adding a rule
-- later is then a migration on this constraint instead of ALTER TYPE, which
-- cannot run inside a transaction on older servers.
ALTER TABLE geofence_pois
    ADD COLUMN IF NOT EXISTS rule TEXT NOT NULL DEFAULT 'watch';

DO $rule_check$
BEGIN
    -- ADD CONSTRAINT has no IF NOT EXISTS, and this service refuses to serve
    -- when a migration fails, so a re-run must not be able to take it down.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'geofence_pois_rule_known'
    ) THEN
        ALTER TABLE geofence_pois
            ADD CONSTRAINT geofence_pois_rule_known
            CHECK (rule IN ('watch', 'stay_inside', 'no_entry'));
    END IF;
END $rule_check$;

COMMENT ON COLUMN geofence_pois.rule IS
  'watch = log arrivals and departures; stay_inside = leaving is the breach (permitted areas, corridors); no_entry = entering is the breach (restricted zones).';

-- Which vehicles the fence applies to.
--
-- NO ROWS MEANS EVERY VEHICLE, not none. That is what every existing fence
-- means, and the opposite reading would switch off monitoring fleet-wide the
-- moment this table appeared.
CREATE TABLE IF NOT EXISTS geofence_vehicles (
    -- ON UPDATE CASCADE is what lets a fence be renamed without losing its
    -- assignments; the API renames with an UPDATE for exactly this reason.
    poi_name   TEXT NOT NULL
        REFERENCES geofence_pois(name) ON DELETE CASCADE ON UPDATE CASCADE,
    -- uuid, not TEXT: vehicles.id has been uuid since 0043, and the older
    -- vehicle_geofence_state table's TEXT column is the shape that made the
    -- ingest path bind text parameters against uuid columns.
    vehicle_id UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (poi_name, vehicle_id)
);

-- "Which fences watch this vehicle" is the question the vehicle detail view
-- asks; the primary key only answers it the other way round.
CREATE INDEX IF NOT EXISTS geofence_vehicles_vehicle_idx
    ON geofence_vehicles (vehicle_id);

COMMENT ON TABLE geofence_vehicles IS
  'Scopes a geofence to particular vehicles. No rows for a fence = it applies to every vehicle.';
