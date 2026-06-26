-- Link a driver record to a platform user account so the driver's phone
-- (companion app) can self-report GPS via POST /api/me/location, authenticated
-- by the driver's platform JWT (matched on its subject UUID). Nullable so
-- existing drivers stay valid until an admin links them; unique (when set) so
-- one platform account maps to at most one driver.
ALTER TABLE drivers ADD COLUMN IF NOT EXISTS platform_user_id UUID;

CREATE UNIQUE INDEX IF NOT EXISTS drivers_platform_user_id_key
    ON drivers (platform_user_id)
    WHERE platform_user_id IS NOT NULL;
