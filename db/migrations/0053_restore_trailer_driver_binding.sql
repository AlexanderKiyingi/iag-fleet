-- 0053: put VEH-NOPLATE-TRAILER-1-SINOTRUCK-TX380 back on its real driver, and
-- remove the probe row that displaced him.
--
-- A CRUD test created a driver with `vehicle` set to this trailer. That write
-- also set vehicles.driver_id to the new driver, displacing Mubangizi Benard,
-- who 0032 and 0046 had assigned. The probe driver then could not be deleted —
-- the referential guard refuses while a vehicle points at it (409, correctly) —
-- so both halves have to be undone together, and in this order.
--
-- This is SQL rather than two API calls because the API is right to refuse both
-- of them. Reassigning Mubangizi Benard returns "driver permit expired or
-- missing": his permit_expiry is still the 2000-01-01 placeholder from the
-- original seed, which 0046 called out and which makes him un-dispatchable.
-- That guard is correct and should not be weakened to fix test debris. The
-- binding itself was created by migration SQL in the first place, so restoring
-- it the same way returns the register to exactly the state 0046 left it in
-- rather than inventing a dispatchable driver.
--
-- Both statements are keyed on exact ids and are safe to re-run: the UPDATE is
-- idempotent, and the DELETE matches nothing once the row is gone.

-- 1. The vehicle points at its real driver again.
UPDATE vehicles
   SET driver_id = fleet_id_to_uuid('DRV-MUBANGIZI-BENARD')
 WHERE id = 'ff3211d1-a80b-3ef7-8f9f-6d2a086d2950'::uuid
   AND driver_id = '1ee43b7c-9e70-4f50-80b8-b53abca577c0'::uuid;

-- 2. Nothing references the probe driver now, so it can go.
DELETE FROM drivers WHERE id = '1ee43b7c-9e70-4f50-80b8-b53abca577c0'::uuid;
