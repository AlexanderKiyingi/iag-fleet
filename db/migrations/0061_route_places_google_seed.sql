-- Google Places support + Uganda / Kenya corridor seed for JMP location dropdowns.
--
-- 0060 created route_places empty. First library is UG + KE only (ports, OSBPs,
-- IAG sites, corridor towns). Coordinates are published WGS84 figures. Live
-- search uses Google Places when GOOGLE_MAPS_API_KEY is set;
-- scripts/collect-route-places-google.mts can attach google_place_id.

ALTER TABLE route_places DROP CONSTRAINT IF EXISTS route_places_source_known;
ALTER TABLE route_places
    ADD CONSTRAINT route_places_source_known
    CHECK (source IN ('manual', 'map', 'nominatim', 'google'));

ALTER TABLE route_places
    ADD COLUMN IF NOT EXISTS google_place_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS route_places_google_place_id_uidx
    ON route_places (google_place_id)
    WHERE google_place_id <> '';

COMMENT ON COLUMN route_places.google_place_id IS
  'Google Places id (ChIJ…). Empty until researched via Google.';
COMMENT ON COLUMN route_places.source IS
  'manual | map | nominatim | google — where the coordinates came from.';

-- Stable seed keys in osm_id (seed:…) so re-runs skip existing rows.
INSERT INTO route_places (name, lat, lng, source, osm_id, display_name, country_code, status, notes)
SELECT v.name, v.lat, v.lng, v.source, v.osm_id, v.display_name, v.country_code, v.status, v.notes
FROM (VALUES
    -- Uganda — IAG / capital corridor
    ('Africa Coffee Park (ACP)', -0.880000::float8, 30.265000::float8, 'manual', 'seed:acp',
     'Africa Coffee Park, Ntungamo, Uganda', 'UG', 'active',
     'IAG site — UG/KE seed'),
    ('Rwashamaire Estate', -0.814000, 30.067000, 'manual', 'seed:rwashamaire',
     'Rwashamaire Estate, Uganda', 'UG', 'active',
     'IAG site — UG/KE seed'),
    ('IAG Kampala HQ', 0.327000, 32.591000, 'manual', 'seed:iag-kampala-hq',
     'IAG Kampala HQ, Kampala, Uganda', 'UG', 'active',
     'IAG site — UG/KE seed'),
    ('Kampala City', 0.347600, 32.582500, 'manual', 'seed:kampala',
     'Kampala, Uganda', 'UG', 'active',
     'City centre — UG/KE seed'),
    ('Namanve Industrial Park', 0.347000, 32.700000, 'manual', 'seed:namanve',
     'Namanve Industrial Park, Mukono, Uganda', 'UG', 'active',
     'Industrial park — UG/KE seed'),
    ('Entebbe International Airport', 0.042400, 32.443500, 'manual', 'seed:entebbe-airport',
     'Entebbe International Airport, Uganda', 'UG', 'active',
     'Airport — UG/KE seed'),
    ('Jinja', 0.424400, 33.204100, 'manual', 'seed:jinja',
     'Jinja, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    ('Iganga', 0.609200, 33.468600, 'manual', 'seed:iganga',
     'Iganga, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    ('Tororo', 0.692800, 34.181000, 'manual', 'seed:tororo',
     'Tororo, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    ('Mbale', 1.082100, 34.175000, 'manual', 'seed:mbale',
     'Mbale, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    ('Soroti', 1.714500, 33.611100, 'manual', 'seed:soroti',
     'Soroti, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    ('Lira', 2.249900, 32.899900, 'manual', 'seed:lira',
     'Lira, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    ('Gulu', 2.774700, 32.299000, 'manual', 'seed:gulu',
     'Gulu, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    ('Masaka', -0.341100, 31.736100, 'manual', 'seed:masaka',
     'Masaka, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    ('Mbarara', -0.607200, 30.654500, 'manual', 'seed:mbarara',
     'Mbarara, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    ('Fort Portal', 0.671000, 30.274800, 'manual', 'seed:fort-portal',
     'Fort Portal, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    ('Hoima', 1.435600, 31.343600, 'manual', 'seed:hoima',
     'Hoima, Uganda', 'UG', 'active',
     'Town — UG/KE seed'),
    -- Uganda–Kenya borders
    ('Malaba Border (URA / KRA)', 0.637370, 34.269520, 'manual', 'seed:malaba-osbp',
     'Malaba One Stop Border Post, Uganda–Kenya', 'UG', 'active',
     'OSBP UG–KE — published GPS 0.63737, 34.26952'),
    ('Busia Border (Uganda)', 0.466940, 34.090000, 'manual', 'seed:busia-ug',
     'Busia OSBP, Uganda–Kenya', 'UG', 'active',
     'OSBP UG–KE — Busia Municipal Council coordinates'),
    -- Kenya — Northern Corridor
    ('Mombasa Port (Kilindini)', -4.055280, 39.651940, 'manual', 'seed:mombasa-port',
     'Kilindini Harbour, Port of Mombasa, Kenya', 'KE', 'active',
     'Port — Wikipedia Kilindini Harbour'),
    ('Mariakani', -3.863300, 39.473600, 'manual', 'seed:mariakani',
     'Mariakani, Kenya', 'KE', 'active',
     'Town / weighbridge corridor — UG/KE seed'),
    ('Voi', -3.396100, 38.556100, 'manual', 'seed:voi',
     'Voi, Kenya', 'KE', 'active',
     'Town — UG/KE seed'),
    ('Nairobi CBD', -1.286400, 36.817200, 'manual', 'seed:nairobi-cbd',
     'Nairobi, Kenya', 'KE', 'active',
     'City centre — UG/KE seed'),
    ('Nairobi ICD Embakasi', -1.335502, 36.878166, 'manual', 'seed:nairobi-icd',
     'ICD Embakasi, Nairobi, Kenya', 'KE', 'active',
     'ICD — BIC facility coordinates'),
    ('Athi River', -1.451800, 36.978100, 'manual', 'seed:athi-river',
     'Athi River, Kenya', 'KE', 'active',
     'Town — UG/KE seed'),
    ('Naivasha', -0.717200, 36.431000, 'manual', 'seed:naivasha',
     'Naivasha, Kenya', 'KE', 'active',
     'Town — UG/KE seed'),
    ('Nakuru', -0.303100, 36.080000, 'manual', 'seed:nakuru',
     'Nakuru, Kenya', 'KE', 'active',
     'Town — UG/KE seed'),
    ('Eldoret', 0.514300, 35.269800, 'manual', 'seed:eldoret',
     'Eldoret, Kenya', 'KE', 'active',
     'Town — UG/KE seed'),
    ('Kitale', 1.015700, 35.006200, 'manual', 'seed:kitale',
     'Kitale, Kenya', 'KE', 'active',
     'Town — UG/KE seed'),
    ('Bungoma', 0.563500, 34.560600, 'manual', 'seed:bungoma',
     'Bungoma, Kenya', 'KE', 'active',
     'Town — UG/KE seed'),
    ('Webuye', 0.616700, 34.766700, 'manual', 'seed:webuye',
     'Webuye, Kenya', 'KE', 'active',
     'Town — UG/KE seed'),
    ('Kisumu Port', -0.091700, 34.768000, 'manual', 'seed:kisumu-port',
     'Port of Kisumu, Kenya', 'KE', 'active',
     'Inland port — UG/KE seed'),
    ('Kericho', -0.367700, 35.283100, 'manual', 'seed:kericho',
     'Kericho, Kenya', 'KE', 'active',
     'Town — UG/KE seed')
) AS v(name, lat, lng, source, osm_id, display_name, country_code, status, notes)
WHERE NOT EXISTS (
    SELECT 1 FROM route_places rp WHERE rp.osm_id = v.osm_id
);
