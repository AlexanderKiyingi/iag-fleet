-- Purge the vehicle and driver register imported by 0032_seed_inspire_vehicles.sql.
--
-- 0032 loaded 37 vehicles and 20 drivers from 'UPDATED LIST OF INSPIRE VEHICLES.xlsx'
-- (27/04/2026) with placeholder permit and contact details (permit_expiry 2000-01-01).
-- They are removed here as part of the platform-wide seed purge; the register is
-- re-established through the UI or CSV import with real permit data.
--
-- Only the exact ids 0032 wrote are targeted, so vehicles and drivers an operator has
-- added since — which share the same VEH-/DRV- prefixes — are untouched. Each row is
-- deleted in its own subtransaction and skipped if trips, fuel records, maintenance or
-- telemetry still reference it, so the migration cannot fail on a foreign key.
--
-- Nothing here touches users, auth_groups, auth_permissions or their links.
--
-- The runner already wraps each migration in its own transaction, so this file does not
-- open one: a COMMIT here would end that transaction early.

DO $$
DECLARE
    demo_drivers TEXT[] := ARRAY[
        'DRV-NYESIGA-ANTONY', 'DRV-BAGAMBA-CHARLES', 'DRV-GUMISIRIZA-HASSAN',
        'DRV-KANYESIGYE-SAM', 'DRV-ASIIMWE-AFRICANO', 'DRV-KABAGAMBE-HERBERT',
        'DRV-MUSIIME-EDSON', 'DRV-NICHOLAS-NUWENYESIGA', 'DRV-BOGERE-JOACHIM',
        'DRV-KUTAMBA-PEDSON', 'DRV-KIIZA-CHARLES', 'DRV-TUKWASIBWE-ALBERT',
        'DRV-MATSIKO-SWALEH', 'DRV-MWESIGYE-ASIIMWE', 'DRV-MUBANGIZI-BENARD',
        'DRV-NUKWASIMIRE-BRUCE', 'DRV-TWEBAZE-STEPHEN', 'DRV-MUSIIMENTA-MUHAMAD',
        'DRV-WAISWA-SYRUS', 'DRV-TURYASIIMA-FRANK'
    ];
    demo_vehicles TEXT[] := ARRAY[
        'VEH-UBP608P', 'VEH-UBR283S', 'VEH-UBR754U',
        'VEH-UBR284S', 'VEH-UBJ326F', 'VEH-UBR358W',
        'VEH-UA324BC', 'VEH-UAX723W', 'VEH-UBG859G',
        'VEH-UAX447V', 'VEH-NOPLATE-PEDESTRIAN-ROLLER-ROLLER', 'VEH-UBE870S',
        'VEH-UA434CK', 'VEH-UA292CL', 'VEH-UA328BV',
        'VEH-UA057BV', 'VEH-UA814BV', 'VEH-UA059BV',
        'VEH-UBE871S', 'VEH-UBF274Z', 'VEH-UA343BV',
        'VEH-NOPLATE-TRAILER-1-SINOTRUCK-TX380', 'VEH-UA403B', 'VEH-NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL',
        'VEH-NOPLATE-NUMBERLESS-NEW-DOZER-CATAPIL-2', 'VEH-UA941BH', 'VEH-UBK589T',
        'VEH-UBJ429L', 'VEH-UBJ458K', 'VEH-UBK590T',
        'VEH-UBJ751P', 'VEH-UBJ022Q', 'VEH-UBJ511N',
        'VEH-UBJ037N', 'VEH-UBJ044N', 'VEH-UG1971W',
        'VEH-UG1776W'
    ];
    row_id TEXT;
    kept   INT := 0;
BEGIN
    -- vehicles.driver_id and drivers.vehicle_id point at each other; break the link
    -- before deleting either side.
    UPDATE vehicles SET driver_id  = NULL WHERE driver_id  = ANY (demo_drivers);
    UPDATE drivers  SET vehicle_id = NULL WHERE vehicle_id = ANY (demo_vehicles);
    FOREACH row_id IN ARRAY demo_drivers
    LOOP
        BEGIN
            DELETE FROM drivers WHERE id = row_id;
        EXCEPTION WHEN foreign_key_violation THEN
            kept := kept + 1;
            RAISE NOTICE 'drivers % still referenced by live operations — kept', row_id;
        END;
    END LOOP;
    FOREACH row_id IN ARRAY demo_vehicles
    LOOP
        BEGIN
            DELETE FROM vehicles WHERE id = row_id;
        EXCEPTION WHEN foreign_key_violation THEN
            kept := kept + 1;
            RAISE NOTICE 'vehicles % still referenced by live operations — kept', row_id;
        END;
    END LOOP;
    IF kept > 0 THEN
        RAISE NOTICE 'purge: % seeded fleet row(s) retained because live records reference them', kept;
    END IF;
END $$;
