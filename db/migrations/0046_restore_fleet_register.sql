-- 0046: restore the vehicle and driver register that 0041 removed.
--
-- 0041 purged the 37 vehicles and 20 drivers 0032 seeded, on the grounds that
-- their permit data was placeholder (permit_expiry 2000-01-01) and the register
-- would be re-imported with real details. The vehicles and people are real -
-- real plates, real names - and that re-import has not happened. An empty
-- register is worse than one carrying placeholder permits: the placeholder only
-- blocks dispatch, while an empty register loses the fleet.
--
-- This restores them. Without it a fresh environment would seed in 0032, purge
-- in 0041 and come up with no fleet at all, which is not what anyone wants and
-- is not what production now looks like.
--
-- Ids go through the same deterministic function 0043 used, so every row lands
-- on exactly the uuid it had before the purge and anything still referencing
-- those ids resolves. ON CONFLICT DO NOTHING so a plate an operator has added
-- since is left alone.
--
-- What this does NOT fix: the permits are still 2000-01-01, so DriverPermitOK
-- still returns false and these drivers remain un-dispatchable. That needs real
-- permit data, which is an import, not a migration - inventing expiry dates
-- would fabricate a compliance record.

CREATE OR REPLACE FUNCTION fleet_id_to_uuid(v TEXT) RETURNS UUID AS $fn$
    SELECT CASE
        WHEN v IS NULL OR v = '' THEN NULL
        WHEN v ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN v::uuid
        ELSE uuid_in(overlay(overlay(md5('iag:fleet:' || v)
                 placing '3' from 13) placing '8' from 17)::cstring)
    END
$fn$ LANGUAGE sql IMMUTABLE;

INSERT INTO vehicles (id, plate, type, make, model, year, vehicle_class, ownership, driver_id, status, location, lat, lng, capacity, last_seen, mech_status) VALUES
  (fleet_id_to_uuid('VEH-UBP608P'), 'UBP608P', 'SINOTRUCK', 'SINOTRUK', '400 ordinary', 0, 'heavy', 'Owned', 'DRV-NYESIGA-ANTONY', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBR283S'), 'UBR283S', 'WHEEL LOADER', 'XCMG', 'XCMG', 0, 'equipment', 'Owned', 'DRV-BAGAMBA-CHARLES', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBR754U'), 'UBR754U', 'SINOTRUCK', 'SINOTRUK', 'TX400', 0, 'heavy', 'Owned', 'DRV-GUMISIRIZA-HASSAN', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBR284S'), 'UBR284S', 'EXCAVATOR', 'XCMG', 'XCMG', 0, 'equipment', 'Owned', 'DRV-KANYESIGYE-SAM', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ326F'), 'UBJ326F', 'ISUZU', 'ISUZU', 'JUSTON', 0, 'heavy', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBR358W'), 'UBR358W', 'ISUZU', 'ISUZU', 'FORWARD', 0, 'heavy', 'Owned', 'DRV-ASIIMWE-AFRICANO', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA324BC'), 'UA324BC', 'ISUZU', 'ISUZU', 'SELF LOADER', 0, 'equipment', 'Owned', 'DRV-KABAGAMBE-HERBERT', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UAX723W'), 'UAX723W', 'SANY ROLLER', 'SANY', 'SANY', 0, 'equipment', 'Owned', 'DRV-MUSIIME-EDSON', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBG859G'), 'UBG859G', 'ISUZU', 'ISUZU', 'BOX BODY', 0, 'heavy', 'Owned', 'DRV-NICHOLAS-NUWENYESIGA', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UAX447V'), 'UAX447V', 'SANY CRANE', 'SANY', 'SANY', 0, 'equipment', 'Owned', 'DRV-BOGERE-JOACHIM', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-NOPLATE-PEDESTRIAN-ROLLER-ROLLER'), 'NOPLATE-PEDESTRIAN-ROLLER-ROLLER', 'ROLLER', '', '', 0, 'equipment', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBE870S'), 'UBE870S', 'ISUZU', 'ISUZU', 'CANTER', 0, 'heavy', 'Owned', 'DRV-KUTAMBA-PEDSON', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA434CK'), 'UA434CK', 'SINOTRUCK', 'SINOTRUK', 'TX371', 0, 'heavy', 'Owned', 'DRV-KIIZA-CHARLES', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA292CL'), 'UA292CL', 'SINOTRUCK', 'SINOTRUK', 'TX371', 0, 'heavy', 'Owned', 'DRV-TUKWASIBWE-ALBERT', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA328BV'), 'UA328BV', 'SINOTRUCK', 'SINOTRUK', 'M7', 0, 'heavy', 'Owned', 'DRV-MATSIKO-SWALEH', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA057BV'), 'UA057BV', 'SINOTRUCK', 'SINOTRUK', 'M7', 0, 'heavy', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA814BV'), 'UA814BV', 'SINOTRUCK', 'SINOTRUK', 'M7', 0, 'heavy', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA059BV'), 'UA059BV', 'FORK LIFT', '', '', 0, 'equipment', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBE871S'), 'UBE871S', 'DRONE(PETROL)', 'TOYOTA', 'TOYOTA', 0, 'light', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBF274Z'), 'UBF274Z', 'DRONE(DIESEL)', 'TOYOTA', 'TOYOTA', 0, 'light', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA343BV'), 'UA343BV', 'SINOTRUCK/WATERBOWSER', 'SINOTRUK', 'TX371', 0, 'heavy', 'Owned', 'DRV-MWESIGYE-ASIIMWE', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-NOPLATE-TRAILER-1-SINOTRUCK-TX380'), 'NOPLATE-TRAILER-1-SINOTRUCK-TX380', 'SINOTRUCK', 'SINOTRUK', 'TX380', 0, 'heavy', 'Owned', 'DRV-MUBANGIZI-BENARD', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA403B'), 'UA403B', 'PICKUP', '', 'HILUX', 0, 'light', 'Owned', 'DRV-NUKWASIMIRE-BRUCE', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL'), 'NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL', 'NEW DOZER', 'CATERPILLAR', 'CATAPILLER D6', 0, 'equipment', 'Owned', 'DRV-TWEBAZE-STEPHEN', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL-2'), 'NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL-2', 'NEW DOZER', 'CATERPILLAR', 'CATAPILLER D8', 0, 'equipment', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UA941BH'), 'UA941BH', 'FUSO', 'FUSO', 'FIGHTER', 0, 'heavy', 'Owned', 'DRV-MUSIIMENTA-MUHAMAD', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBK589T'), 'UBK589T', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ429L'), 'UBJ429L', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ458K'), 'UBJ458K', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBK590T'), 'UBK590T', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ751P'), 'UBJ751P', 'GRADER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ022Q'), 'UBJ022Q', 'EXCAVATOR', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ511N'), 'UBJ511N', 'GRADER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ037N'), 'UBJ037N', 'DOZER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UBJ044N'), 'UBJ044N', 'DOZER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UG1971W'), 'UG1971W', 'BACKHOE', 'KOMATSU', 'KOMATSU', 0, 'equipment', 'MOW', 'DRV-WAISWA-SYRUS', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  (fleet_id_to_uuid('VEH-UG1776W'), 'UG1776W', 'DOZER', 'KOMATSU', 'KOMATSU', 0, 'equipment', 'MOW', 'DRV-TURYASIIMA-FRANK', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational')
ON CONFLICT (id) DO NOTHING;

INSERT INTO drivers (id, name, initials, role, phone, permit_no, permit_class, permit_expiry, home_region, status, vehicle_id) VALUES
  (fleet_id_to_uuid('DRV-NYESIGA-ANTONY'), 'Nyesiga Antony', 'NA', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UBP608P')),
  (fleet_id_to_uuid('DRV-BAGAMBA-CHARLES'), 'Bagamba Charles', 'BC', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UBR283S')),
  (fleet_id_to_uuid('DRV-GUMISIRIZA-HASSAN'), 'Gumisiriza Hassan', 'GH', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UBR754U')),
  (fleet_id_to_uuid('DRV-KANYESIGYE-SAM'), 'Kanyesigye Sam', 'KS', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UBR284S')),
  (fleet_id_to_uuid('DRV-ASIIMWE-AFRICANO'), 'Asiimwe Africano', 'AA', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UBR358W')),
  (fleet_id_to_uuid('DRV-KABAGAMBE-HERBERT'), 'Kabagambe Herbert', 'KH', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UA324BC')),
  (fleet_id_to_uuid('DRV-MUSIIME-EDSON'), 'Musiime Edson', 'ME', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UAX723W')),
  (fleet_id_to_uuid('DRV-NICHOLAS-NUWENYESIGA'), 'Nicholas Nuwenyesiga', 'NN', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UBG859G')),
  (fleet_id_to_uuid('DRV-BOGERE-JOACHIM'), 'Bogere Joachim', 'BJ', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UAX447V')),
  (fleet_id_to_uuid('DRV-KUTAMBA-PEDSON'), 'Kutamba Pedson', 'KP', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UBE870S')),
  (fleet_id_to_uuid('DRV-KIIZA-CHARLES'), 'Kiiza Charles', 'KC', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UA434CK')),
  (fleet_id_to_uuid('DRV-TUKWASIBWE-ALBERT'), 'Tukwasibwe Albert', 'TA', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UA292CL')),
  (fleet_id_to_uuid('DRV-MATSIKO-SWALEH'), 'Matsiko Swaleh', 'MS', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UA328BV')),
  (fleet_id_to_uuid('DRV-MWESIGYE-ASIIMWE'), 'Mwesigye Asiimwe', 'MA', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UA343BV')),
  (fleet_id_to_uuid('DRV-MUBANGIZI-BENARD'), 'Mubangizi Benard', 'MB', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-NOPLATE-TRAILER-1-SINOTRUCK-TX380')),
  (fleet_id_to_uuid('DRV-NUKWASIMIRE-BRUCE'), 'Nukwasimire Bruce', 'NB', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UA403B')),
  (fleet_id_to_uuid('DRV-TWEBAZE-STEPHEN'), 'Twebaze Stephen', 'TS', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL')),
  (fleet_id_to_uuid('DRV-MUSIIMENTA-MUHAMAD'), 'Musiimenta Muhamad', 'MM', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UA941BH')),
  (fleet_id_to_uuid('DRV-WAISWA-SYRUS'), 'Waiswa Syrus', 'WS', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UG1971W')),
  (fleet_id_to_uuid('DRV-TURYASIIMA-FRANK'), 'Turyasiima Frank', 'TF', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', fleet_id_to_uuid('VEH-UG1776W'))
ON CONFLICT (id) DO NOTHING;
