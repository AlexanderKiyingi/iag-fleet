-- 0046: vehicles.driver_id — the reference 0043 missed
--
-- 0043 converted every entity id and foreign key from TEXT to UUID and listed,
-- carefully, the columns it deliberately left alone. vehicles.driver_id is in
-- neither list. It stayed TEXT while drivers.id became a uuid, so every vehicle
-- that had a driver now points at a row that cannot be found:
--
--   vehicles.driver_id = 'DRV-MUBANGIZI-BENARD'
--   drivers.id         = 68ba7bbb-eb30-3b5a-800c-28aab975f31d
--
-- Measured against production on 2026-08-28: 20 of 42 vehicles carry a driver,
-- and 0 of those 20 resolve. The vehicle list shows a raw slug where the
-- driver's name belongs, and anything that joins the two gets nothing.
--
-- The repair is exact rather than a guess. fleet_id_to_uuid was deterministic —
-- md5('iag:fleet:' || id) shaped into a v3 uuid — and drivers.id was converted
-- from the very strings vehicles.driver_id still holds. Applying the same
-- function to this column lands on the same uuid. Verified for all 20 links
-- before writing this: every one maps onto a driver, and the names line up.
--
-- 0043 dropped the function at the end of its run, so it is recreated here and
-- dropped again. Same definition, copied rather than reimplemented.

CREATE OR REPLACE FUNCTION fleet_id_to_uuid(v TEXT) RETURNS UUID AS $fn$
    SELECT CASE
        WHEN v IS NULL OR v = '' THEN NULL
        WHEN v ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN v::uuid
        ELSE uuid_in(overlay(overlay(md5('iag:fleet:' || v)
                 placing '3' from 13) placing '8' from 17)::cstring)
    END
$fn$ LANGUAGE sql IMMUTABLE;

-- Any driver_id that is already a uuid passes through untouched, so this is
-- safe to run against a database where some rows were written after the
-- conversion.
ALTER TABLE vehicles
    ALTER COLUMN driver_id TYPE UUID USING fleet_id_to_uuid(driver_id);

-- An empty string was the old "no driver". It cannot survive as a uuid, and
-- fleet_id_to_uuid already returns NULL for it — this is belt and braces for
-- rows written between 0043 and this migration.
UPDATE vehicles SET driver_id = NULL WHERE driver_id IS NOT NULL AND driver_id::text = '';

-- The reference is real, so declare it. ON DELETE SET NULL matches
-- iot_devices.vehicle_id, restored the same way in 0043: removing a driver
-- unassigns the vehicle rather than deleting it.
ALTER TABLE vehicles DROP CONSTRAINT IF EXISTS vehicles_driver_id_fkey;
ALTER TABLE vehicles ADD CONSTRAINT vehicles_driver_id_fkey
    FOREIGN KEY (driver_id) REFERENCES drivers(id) ON DELETE SET NULL
    NOT VALID;

-- NOT VALID above, then validated separately: validation takes a lighter lock
-- than adding an already-checked constraint, and a row pointing at a driver
-- that no longer exists should surface as a validation failure to look at
-- rather than silently blocking the migration.
ALTER TABLE vehicles VALIDATE CONSTRAINT vehicles_driver_id_fkey;

CREATE INDEX IF NOT EXISTS vehicles_driver_id_idx ON vehicles (driver_id);

DROP FUNCTION fleet_id_to_uuid(TEXT);
