-- Ntinda as its own hub. HAULA used to label Africa Coffee Park at these
-- coordinates (greater Kampala); ACP itself is in Ntungamo (~-0.88, 30.265).
INSERT INTO route_places (name, lat, lng, source, osm_id, display_name, country_code, status, notes, category)
SELECT v.name, v.lat, v.lng, v.source, v.osm_id, v.display_name, v.country_code, v.status, v.notes, v.category
FROM (VALUES
    ('Ntinda', 0.360800, 32.619400, 'manual', 'seed:ntinda',
     'Ntinda, Kampala, Uganda', 'UG', 'active',
     'Kampala suburb — distinct from Africa Coffee Park (Ntungamo)', 'hub')
) AS v(name, lat, lng, source, osm_id, display_name, country_code, status, notes, category)
WHERE NOT EXISTS (
    SELECT 1 FROM route_places rp WHERE rp.osm_id = v.osm_id
);
