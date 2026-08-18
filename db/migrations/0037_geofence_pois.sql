-- Geofence points of interest, moved out of Go and into data.
--
-- The six sites were a hardcoded slice in iot/geofence_pois.go, so adding a
-- customer site or nudging a radius meant editing Go, releasing
-- iag-telemetry-gateway, bumping the pin in iag-fleet and redeploying both.
-- That is an absurd amount of ceremony for "the new depot is 400 m across",
-- and it guarantees the geofences drift away from where the trucks actually go.
--
-- The gateway falls back to the built-in list when this table is empty, so an
-- unmigrated or unseeded database behaves exactly as before.

CREATE TABLE IF NOT EXISTS geofence_pois (
    name       TEXT PRIMARY KEY,
    lat        DOUBLE PRECISION NOT NULL,
    lng        DOUBLE PRECISION NOT NULL,
    type       TEXT NOT NULL DEFAULT 'site',
    radius_km  DOUBLE PRECISION NOT NULL,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT geofence_pois_radius_positive CHECK (radius_km > 0),
    CONSTRAINT geofence_pois_lat_range CHECK (lat BETWEEN -90 AND 90),
    CONSTRAINT geofence_pois_lng_range CHECK (lng BETWEEN -180 AND 180)
);

CREATE INDEX IF NOT EXISTS geofence_pois_active_idx ON geofence_pois (is_active);

COMMENT ON TABLE geofence_pois IS
  'Circular geofences evaluated per ping. Empty table = the gateway uses its built-in defaults.';
COMMENT ON COLUMN geofence_pois.name IS
  'Primary key and the label used in safety events; renaming a POI starts its enter/exit state fresh.';
COMMENT ON COLUMN geofence_pois.radius_km IS
  'Geofence radius in kilometres. Must be positive — a zero radius would never trigger.';

-- Seed the previously-hardcoded set so behaviour is identical on upgrade.
INSERT INTO geofence_pois (name, lat, lng, type, radius_km) VALUES
    ('Africa Coffee Park (ACP)', -0.880, 30.265, 'iag',    0.6),
    ('Rwashamaire Estate',       -0.814, 30.067, 'iag',    0.4),
    ('IAG Kampala HQ',            0.327, 32.591, 'iag',    0.3),
    ('Mombasa Port',             -4.050, 39.667, 'port',   1.5),
    ('Dar es Salaam Port',       -6.792, 39.208, 'port',   1.5),
    ('Malaba Border (URA)',       0.637, 34.265, 'border', 0.5)
ON CONFLICT (name) DO NOTHING;
