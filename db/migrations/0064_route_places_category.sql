-- Category on JMP locations so operators can filter gas stations, hotels,
-- warehouses, etc. when picking From / To / stops.

ALTER TABLE route_places
    ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'place';

CREATE INDEX IF NOT EXISTS route_places_category_idx ON route_places (category);
CREATE INDEX IF NOT EXISTS route_places_country_category_idx
    ON route_places (country_code, category)
    WHERE status = 'active';

COMMENT ON COLUMN route_places.category IS
  'place | town | gas_station | hotel | motel | restaurant | supermarket | market | warehouse | …';

-- Existing corridor seeds are towns / sites, not POIs.
UPDATE route_places
SET category = 'town'
WHERE category = 'place'
  AND osm_id LIKE 'seed:%'
  AND status = 'active';
