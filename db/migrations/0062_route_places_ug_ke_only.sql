-- Keep the JMP location library focused on Uganda and Kenya for now.
-- Deactivates any earlier seed rows outside UG/KE (TZ towns, non-KE borders).

UPDATE route_places
SET status = 'inactive',
    notes = CASE
        WHEN notes LIKE '%deactivated: UG/KE focus%' THEN notes
        ELSE trim(both FROM notes || ' | deactivated: UG/KE focus')
    END,
    updated_at = NOW()
WHERE status = 'active'
  AND (
      country_code NOT IN ('UG', 'KE')
      OR osm_id IN (
          'seed:dar-port',
          'seed:arusha',
          'seed:tanga-port',
          'seed:mutukula',
          'seed:elegu-nimule',
          'seed:katuna',
          'seed:mirama-hills'
      )
  );
