-- Named waypoints for journey planning (JMP From / To / stops).
--
-- Distinct from geofence_pois: these are reusable map pins researched online
-- (Nominatim / OSM) or dropped on the map — not evaluated on telemetry pings.
-- JMP routeSummary still embeds `@lat,lng` (or `Name@lat,lng`); this table is
-- the shared library operators pick from so the same border / depot / port is
-- not re-researched on every plan.

CREATE TABLE IF NOT EXISTS route_places (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    lat           DOUBLE PRECISION NOT NULL,
    lng           DOUBLE PRECISION NOT NULL,
    source        TEXT NOT NULL DEFAULT 'manual',
    osm_id        TEXT NOT NULL DEFAULT '',
    display_name  TEXT NOT NULL DEFAULT '',
    country_code  TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'active',
    notes         TEXT NOT NULL DEFAULT '',
    CONSTRAINT route_places_lat_range CHECK (lat BETWEEN -90 AND 90),
    CONSTRAINT route_places_lng_range CHECK (lng BETWEEN -180 AND 180),
    CONSTRAINT route_places_source_known CHECK (source IN ('manual', 'map', 'nominatim')),
    CONSTRAINT route_places_name_nonempty CHECK (length(trim(name)) > 0)
);

-- One OSM object → one row. Empty osm_id (map / manual pins) can repeat.
CREATE UNIQUE INDEX IF NOT EXISTS route_places_osm_id_uidx
    ON route_places (osm_id)
    WHERE osm_id <> '';

CREATE INDEX IF NOT EXISTS route_places_name_idx ON route_places (lower(name));
CREATE INDEX IF NOT EXISTS route_places_status_idx ON route_places (status);

ALTER TABLE route_places ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE route_places ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

DROP TRIGGER IF EXISTS route_places_touch ON route_places;
CREATE TRIGGER route_places_touch
    BEFORE INSERT OR UPDATE ON route_places
    FOR EACH ROW EXECUTE FUNCTION touch_row();

CREATE INDEX IF NOT EXISTS route_places_updated_at_idx ON route_places (updated_at DESC);

COMMENT ON TABLE route_places IS
  'Reusable JMP waypoints. Not geofences — arrival/exit is never evaluated here.';
COMMENT ON COLUMN route_places.osm_id IS
  'Stable OSM key osm:{type}:{id} from Nominatim; empty for free map pins.';
COMMENT ON COLUMN route_places.source IS
  'manual | map | nominatim — where the coordinates came from.';
