-- Records where a vehicle's latest position fix came from so the live map can
-- distinguish a phone (driver companion app) from a hardware tracker. Set on
-- every ping by SyncVehicleFromPing: 'mobile' when the ping has no device,
-- 'device' otherwise. Empty until the vehicle's first fix.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS last_fix_source TEXT NOT NULL DEFAULT '';
