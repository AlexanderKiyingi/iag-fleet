-- 0052: restore the vehicle register after an operator-tool PUT cleared it.
--
-- On 2026-09-11 a records PUT to fleet/vehicles ran in `replace` mode carrying a
-- single row. `replace` means "the payload is the whole collection", so the
-- route deleted every vehicle absent from it (route.ts: "absent means deleted")
-- and the register went from 37 rows to 1. The delete is a hard DELETE - there
-- is no soft-delete column on vehicles - so the rows are gone from Postgres.
--
-- Drivers were untouched: drivers.vehicle_id carries no FK onto vehicles, so
-- the 20 driver->vehicle bindings survived the delete as dangling references.
-- That is what makes this restorable in place rather than a re-import: the ids
-- come from the same deterministic fleet_id_to_uuid() that 0043 and 0046 used,
-- so every restored vehicle lands on exactly the uuid it had, and those 20
-- bindings (plus the pm-schedules referencing them) resolve again.
--
-- This re-runs 0046's vehicle block verbatim. 0046 itself cannot be re-applied:
-- migrations are forward-only and it is already recorded as run. ON CONFLICT DO
-- NOTHING, so the one row that survived and anything an operator has added
-- since are left alone.
--
-- The permit caveat from 0046 still stands: permit_expiry remains 2000-01-01 on
-- the restored drivers, so they stay un-dispatchable until real permit data is
-- imported. Inventing expiry dates would fabricate a compliance record.

CREATE OR REPLACE FUNCTION fleet_id_to_uuid(v TEXT) RETURNS UUID AS $fn$
    SELECT CASE
        WHEN v IS NULL OR v = '' THEN NULL
        WHEN v ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN v::uuid
        ELSE uuid_in(overlay(overlay(md5('iag:fleet:' || v)
                 placing '3' from 13) placing '8' from 17)::cstring)
    END
$fn$ LANGUAGE sql IMMUTABLE;

-- driver_id went to UUID in 0046_vehicles_driver_id_uuid with an FK onto
-- drivers(id), so 0046_restore's raw 'DRV-…' literals would now fail with 22P02,
-- and a failed migration is an outage (autoMigrate refuses to serve). Each one is
-- resolved through a scalar subquery rather than a bare cast: if a driver row is
-- missing the subquery yields NULL, so the restore cannot trip the FK and take
-- the service down with it.
INSERT INTO vehicles (id, plate, type, make, model, year, vehicle_class, ownership, driver_id, status, location, lat, lng, capacity, last_seen, mech_status) VALUES
  (fleet_id_to_uuid('VEH-UBP608P'), 'UBP608P', 'SINOTRUCK', 'SINOTRUK', '400 ordinary', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-NYESIGA-ANTONY')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBR283S'), 'UBR283S', 'WHEEL LOADER', 'XCMG', 'XCMG', 0, 'equipment', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-BAGAMBA-CHARLES')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBR754U'), 'UBR754U', 'SINOTRUCK', 'SINOTRUK', 'TX400', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-GUMISIRIZA-HASSAN')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBR284S'), 'UBR284S', 'EXCAVATOR', 'XCMG', 'XCMG', 0, 'equipment', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-KANYESIGYE-SAM')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ326F'), 'UBJ326F', 'ISUZU', 'ISUZU', 'JUSTON', 0, 'heavy', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBR358W'), 'UBR358W', 'ISUZU', 'ISUZU', 'FORWARD', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-ASIIMWE-AFRICANO')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA324BC'), 'UA324BC', 'ISUZU', 'ISUZU', 'SELF LOADER', 0, 'equipment', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-KABAGAMBE-HERBERT')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UAX723W'), 'UAX723W', 'SANY ROLLER', 'SANY', 'SANY', 0, 'equipment', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-MUSIIME-EDSON')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBG859G'), 'UBG859G', 'ISUZU', 'ISUZU', 'BOX BODY', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-NICHOLAS-NUWENYESIGA')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UAX447V'), 'UAX447V', 'SANY CRANE', 'SANY', 'SANY', 0, 'equipment', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-BOGERE-JOACHIM')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-NOPLATE-PEDESTRIAN-ROLLER-ROLLER'), 'NOPLATE-PEDESTRIAN-ROLLER-ROLLER', 'ROLLER', '', '', 0, 'equipment', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBE870S'), 'UBE870S', 'ISUZU', 'ISUZU', 'CANTER', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-KUTAMBA-PEDSON')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA434CK'), 'UA434CK', 'SINOTRUCK', 'SINOTRUK', 'TX371', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-KIIZA-CHARLES')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA292CL'), 'UA292CL', 'SINOTRUCK', 'SINOTRUK', 'TX371', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-TUKWASIBWE-ALBERT')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA328BV'), 'UA328BV', 'SINOTRUCK', 'SINOTRUK', 'M7', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-MATSIKO-SWALEH')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA057BV'), 'UA057BV', 'SINOTRUCK', 'SINOTRUK', 'M7', 0, 'heavy', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA814BV'), 'UA814BV', 'SINOTRUCK', 'SINOTRUK', 'M7', 0, 'heavy', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA059BV'), 'UA059BV', 'FORK LIFT', '', '', 0, 'equipment', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBE871S'), 'UBE871S', 'DRONE(PETROL)', 'TOYOTA', 'TOYOTA', 0, 'light', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBF274Z'), 'UBF274Z', 'DRONE(DIESEL)', 'TOYOTA', 'TOYOTA', 0, 'light', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA343BV'), 'UA343BV', 'SINOTRUCK/WATERBOWSER', 'SINOTRUK', 'TX371', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-MWESIGYE-ASIIMWE')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-NOPLATE-TRAILER-1-SINOTRUCK-TX380'), 'NOPLATE-TRAILER-1-SINOTRUCK-TX380', 'SINOTRUCK', 'SINOTRUK', 'TX380', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-MUBANGIZI-BENARD')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA403B'), 'UA403B', 'PICKUP', '', 'HILUX', 0, 'light', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-NUKWASIMIRE-BRUCE')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL'), 'NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL', 'NEW DOZER', 'CATERPILLAR', 'CATAPILLER D6', 0, 'equipment', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-TWEBAZE-STEPHEN')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL-2'), 'NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL-2', 'NEW DOZER', 'CATERPILLAR', 'CATAPILLER D8', 0, 'equipment', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA941BH'), 'UA941BH', 'FUSO', 'FUSO', 'FIGHTER', 0, 'heavy', 'Owned', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-MUSIIMENTA-MUHAMAD')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBK589T'), 'UBK589T', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ429L'), 'UBJ429L', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ458K'), 'UBJ458K', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBK590T'), 'UBK590T', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ751P'), 'UBJ751P', 'GRADER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ022Q'), 'UBJ022Q', 'EXCAVATOR', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ511N'), 'UBJ511N', 'GRADER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ037N'), 'UBJ037N', 'DOZER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ044N'), 'UBJ044N', 'DOZER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UG1971W'), 'UG1971W', 'BACKHOE', 'KOMATSU', 'KOMATSU', 0, 'equipment', 'MOW', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-WAISWA-SYRUS')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UG1776W'), 'UG1776W', 'DOZER', 'KOMATSU', 'KOMATSU', 0, 'equipment', 'MOW', (SELECT d.id FROM drivers d WHERE d.id = fleet_id_to_uuid('DRV-TURYASIIMA-FRANK')), 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational')
ON CONFLICT (id) DO NOTHING;

-- Remove the probe row whose `replace` PUT caused the clearance. Matched on the
-- primary key alone, which is exact: the id was service-assigned on insert, so
-- it names that one row and nothing else. It is deliberately NOT matched on
-- plate as well — the probe carried no plate, and guessing at how the adapter
-- mapped its fields would risk a WHERE that quietly matches nothing.
DELETE FROM vehicles WHERE id = '950aae6a-082e-4167-9f87-e38e389a0291'::uuid;
