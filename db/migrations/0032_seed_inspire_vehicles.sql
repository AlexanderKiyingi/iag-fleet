-- Seed: Inspire Africa vehicle fleet + drivers (source: 'UPDATED LIST OF INSPIRE VEHICLES.xlsx', dated 27/04/2026).
-- One-time import. Idempotent via ON CONFLICT DO NOTHING so a re-run (or a plate already
-- added via the UI) is skipped rather than erroring. Driver records carry placeholder
-- permit/phone/region data (permit_expiry 2000-01-01 flags them as needing real details).

-- ── Drivers (named drivers from the sheet; 'UNATTACHED' rows get no record) ──
INSERT INTO drivers (id, name, initials, role, phone, permit_no, permit_class, permit_expiry, home_region, status, vehicle_id) VALUES
  ('DRV-NYESIGA-ANTONY', 'Nyesiga Antony', 'NA', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UBP608P'),
  ('DRV-BAGAMBA-CHARLES', 'Bagamba Charles', 'BC', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UBR283S'),
  ('DRV-GUMISIRIZA-HASSAN', 'Gumisiriza Hassan', 'GH', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UBR754U'),
  ('DRV-KANYESIGYE-SAM', 'Kanyesigye Sam', 'KS', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UBR284S'),
  ('DRV-ASIIMWE-AFRICANO', 'Asiimwe Africano', 'AA', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UBR358W'),
  ('DRV-KABAGAMBE-HERBERT', 'Kabagambe Herbert', 'KH', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UA324BC'),
  ('DRV-MUSIIME-EDSON', 'Musiime Edson', 'ME', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UAX723W'),
  ('DRV-NICHOLAS-NUWENYESIGA', 'Nicholas Nuwenyesiga', 'NN', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UBG859G'),
  ('DRV-BOGERE-JOACHIM', 'Bogere Joachim', 'BJ', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UAX447V'),
  ('DRV-KUTAMBA-PEDSON', 'Kutamba Pedson', 'KP', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UBE870S'),
  ('DRV-KIIZA-CHARLES', 'Kiiza Charles', 'KC', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UA434CK'),
  ('DRV-TUKWASIBWE-ALBERT', 'Tukwasibwe Albert', 'TA', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UA292CL'),
  ('DRV-MATSIKO-SWALEH', 'Matsiko Swaleh', 'MS', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UA328BV'),
  ('DRV-MWESIGYE-ASIIMWE', 'Mwesigye Asiimwe', 'MA', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UA343BV'),
  ('DRV-MUBANGIZI-BENARD', 'Mubangizi Benard', 'MB', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-NOPLATE-TRAILER-1-SINOTRUCK-TX380'),
  ('DRV-NUKWASIMIRE-BRUCE', 'Nukwasimire Bruce', 'NB', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UA403B'),
  ('DRV-TWEBAZE-STEPHEN', 'Twebaze Stephen', 'TS', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL'),
  ('DRV-MUSIIMENTA-MUHAMAD', 'Musiimenta Muhamad', 'MM', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UA941BH'),
  ('DRV-WAISWA-SYRUS', 'Waiswa Syrus', 'WS', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UG1971W'),
  ('DRV-TURYASIIMA-FRANK', 'Turyasiima Frank', 'TF', 'Driver', '', '', '', DATE '2000-01-01', 'Unknown', 'off-duty', 'VEH-UG1776W')
ON CONFLICT DO NOTHING;

-- ── Vehicles ──
INSERT INTO vehicles (id, plate, type, make, model, year, vehicle_class, ownership, driver_id, status, location, lat, lng, capacity, last_seen, mech_status) VALUES
  ('VEH-UBP608P', 'UBP608P', 'SINOTRUCK', 'SINOTRUK', '400 ordinary', 0, 'heavy', 'Owned', 'DRV-NYESIGA-ANTONY', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBR283S', 'UBR283S', 'WHEEL LOADER', 'XCMG', 'XCMG', 0, 'equipment', 'Owned', 'DRV-BAGAMBA-CHARLES', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBR754U', 'UBR754U', 'SINOTRUCK', 'SINOTRUK', 'TX400', 0, 'heavy', 'Owned', 'DRV-GUMISIRIZA-HASSAN', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBR284S', 'UBR284S', 'EXCAVATOR', 'XCMG', 'XCMG', 0, 'equipment', 'Owned', 'DRV-KANYESIGYE-SAM', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBJ326F', 'UBJ326F', 'ISUZU', 'ISUZU', 'JUSTON', 0, 'heavy', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBR358W', 'UBR358W', 'ISUZU', 'ISUZU', 'FORWARD', 0, 'heavy', 'Owned', 'DRV-ASIIMWE-AFRICANO', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UA324BC', 'UA324BC', 'ISUZU', 'ISUZU', 'SELF LOADER', 0, 'equipment', 'Owned', 'DRV-KABAGAMBE-HERBERT', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UAX723W', 'UAX723W', 'SANY ROLLER', 'SANY', 'SANY', 0, 'equipment', 'Owned', 'DRV-MUSIIME-EDSON', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBG859G', 'UBG859G', 'ISUZU', 'ISUZU', 'BOX BODY', 0, 'heavy', 'Owned', 'DRV-NICHOLAS-NUWENYESIGA', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UAX447V', 'UAX447V', 'SANY CRANE', 'SANY', 'SANY', 0, 'equipment', 'Owned', 'DRV-BOGERE-JOACHIM', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-NOPLATE-PEDESTRIAN-ROLLER-ROLLER', 'NOPLATE-PEDESTRIAN-ROLLER-ROLLER', 'ROLLER', '', '', 0, 'equipment', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBE870S', 'UBE870S', 'ISUZU', 'ISUZU', 'CANTER', 0, 'heavy', 'Owned', 'DRV-KUTAMBA-PEDSON', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UA434CK', 'UA434CK', 'SINOTRUCK', 'SINOTRUK', 'TX371', 0, 'heavy', 'Owned', 'DRV-KIIZA-CHARLES', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UA292CL', 'UA292CL', 'SINOTRUCK', 'SINOTRUK', 'TX371', 0, 'heavy', 'Owned', 'DRV-TUKWASIBWE-ALBERT', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UA328BV', 'UA328BV', 'SINOTRUCK', 'SINOTRUK', 'M7', 0, 'heavy', 'Owned', 'DRV-MATSIKO-SWALEH', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UA057BV', 'UA057BV', 'SINOTRUCK', 'SINOTRUK', 'M7', 0, 'heavy', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UA814BV', 'UA814BV', 'SINOTRUCK', 'SINOTRUK', 'M7', 0, 'heavy', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UA059BV', 'UA059BV', 'FORK LIFT', '', '', 0, 'equipment', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBE871S', 'UBE871S', 'DRONE(PETROL)', 'TOYOTA', 'TOYOTA', 0, 'light', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBF274Z', 'UBF274Z', 'DRONE(DIESEL)', 'TOYOTA', 'TOYOTA', 0, 'light', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UA343BV', 'UA343BV', 'SINOTRUCK/WATERBOWSER', 'SINOTRUK', 'TX371', 0, 'heavy', 'Owned', 'DRV-MWESIGYE-ASIIMWE', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-NOPLATE-TRAILER-1-SINOTRUCK-TX380', 'NOPLATE-TRAILER-1-SINOTRUCK-TX380', 'SINOTRUCK', 'SINOTRUK', 'TX380', 0, 'heavy', 'Owned', 'DRV-MUBANGIZI-BENARD', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UA403B', 'UA403B', 'PICKUP', '', 'HILUX', 0, 'light', 'Owned', 'DRV-NUKWASIMIRE-BRUCE', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL', 'NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL', 'NEW DOZER', 'CATERPILLAR', 'CATAPILLER D6', 0, 'equipment', 'Owned', 'DRV-TWEBAZE-STEPHEN', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL-2', 'NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL-2', 'NEW DOZER', 'CATERPILLAR', 'CATAPILLER D8', 0, 'equipment', 'Owned', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UA941BH', 'UA941BH', 'FUSO', 'FUSO', 'FIGHTER', 0, 'heavy', 'Owned', 'DRV-MUSIIMENTA-MUHAMAD', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBK589T', 'UBK589T', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBJ429L', 'UBJ429L', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBJ458K', 'UBJ458K', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBK590T', 'UBK590T', 'SINO TRUCK', 'SINOTRUK', '371', 0, 'heavy', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBJ751P', 'UBJ751P', 'GRADER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBJ022Q', 'UBJ022Q', 'EXCAVATOR', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBJ511N', 'UBJ511N', 'GRADER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBJ037N', 'UBJ037N', 'DOZER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UBJ044N', 'UBJ044N', 'DOZER', 'SANY', 'SANNY', 0, 'equipment', 'Hired', NULL, 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UG1971W', 'UG1971W', 'BACKHOE', 'KOMATSU', 'KOMATSU', 0, 'equipment', 'MOW', 'DRV-WAISWA-SYRUS', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational'),
  ('VEH-UG1776W', 'UG1776W', 'DOZER', 'KOMATSU', 'KOMATSU', 0, 'equipment', 'MOW', 'DRV-TURYASIIMA-FRANK', 'idle', '', 0, 0, '', TIMESTAMPTZ '2026-04-27T00:00:00Z', 'operational')
ON CONFLICT DO NOTHING;
