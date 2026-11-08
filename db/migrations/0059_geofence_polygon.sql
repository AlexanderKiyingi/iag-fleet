-- Polygon geofences (SinoTrack / WhatsGPS parity).
--
-- Circles stay the default. A polygon fence stores WGS84 vertices as JSONB
-- [{ "lat": …, "lng": … }, …] with at least three points. lat/lng remain the
-- centroid used for map focus and as a cheap pre-filter; radius_km for a
-- polygon is the farthest vertex from that centroid (still > 0) so existing
-- CHECK and map tooling keep working without a second code path for "no radius".

ALTER TABLE geofence_pois
    ADD COLUMN IF NOT EXISTS shape TEXT NOT NULL DEFAULT 'circle',
    ADD COLUMN IF NOT EXISTS vertices JSONB;

ALTER TABLE geofence_pois DROP CONSTRAINT IF EXISTS geofence_pois_radius_positive;
ALTER TABLE geofence_pois
    ADD CONSTRAINT geofence_pois_radius_positive CHECK (radius_km > 0);

ALTER TABLE geofence_pois DROP CONSTRAINT IF EXISTS geofence_pois_shape_known;
ALTER TABLE geofence_pois
    ADD CONSTRAINT geofence_pois_shape_known CHECK (shape IN ('circle', 'polygon'));

ALTER TABLE geofence_pois DROP CONSTRAINT IF EXISTS geofence_pois_polygon_vertices;
ALTER TABLE geofence_pois
    ADD CONSTRAINT geofence_pois_polygon_vertices CHECK (
        shape <> 'polygon'
        OR (jsonb_typeof(vertices) = 'array' AND jsonb_array_length(vertices) >= 3)
    );

COMMENT ON COLUMN geofence_pois.shape IS
  'circle (lat/lng + radius_km) or polygon (vertices JSONB; lat/lng = centroid).';
COMMENT ON COLUMN geofence_pois.vertices IS
  'Polygon ring as [{lat,lng},…]. NULL for circles.';
