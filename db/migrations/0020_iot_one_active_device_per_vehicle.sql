-- Enforce at most one ACTIVE IoT device per vehicle. Retired/inactive device
-- rows may keep their old vehicle_id for history (the index is partial on
-- is_active), so swapping a unit is: deactivate the old row, bind the new one.
--
-- Before adding the index, resolve any pre-existing duplicates so creation
-- cannot fail: per vehicle, keep the most recently seen active device and
-- deactivate the rest (deterministic, non-destructive — rows are demoted, not
-- deleted). Idempotent: once there are no duplicates the UPDATE is a no-op and
-- the index already exists.
WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY vehicle_id
               ORDER BY last_seen DESC NULLS LAST, id DESC
           ) AS rn
    FROM iot_devices
    WHERE vehicle_id IS NOT NULL AND is_active
)
UPDATE iot_devices d
SET is_active = FALSE
FROM ranked r
WHERE d.id = r.id AND r.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS iot_devices_one_active_per_vehicle
    ON iot_devices (vehicle_id)
    WHERE vehicle_id IS NOT NULL AND is_active;
