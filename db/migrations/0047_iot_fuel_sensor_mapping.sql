-- Per-device fuel sensor mapping.
--
-- The Teltonika gateway extracted fuel from exactly one hardcoded IO ID:
--
--     ioIDFuelPct uint16 = 89   // value / 10 -> percent
--
-- which is CAN-bus fuel level in percent, available only on a unit wired to a
-- CAN adapter. That is the minority of a real fleet. The two sensors operators
-- actually fit are unreadable:
--
--   * Analog senders on AIN1 — the standard retrofit for a truck with no CAN.
--     Arrives as millivolts, needs a per-tank scale.
--   * LLS capacitive probes over RS232/RS485 — the anti-theft fuel-monitoring
--     choice, and usually the whole reason a sensor is fitted at all. Arrives
--     as a raw count (commonly 0-4095), needs a per-tank scale.
--
-- Both were already being stored: the gateway writes the full fixed-width IO
-- map into telemetry_timeseries.raw, so history can be backfilled once the
-- right ID is known. Only the extraction was missing.
--
-- Rather than add two more constants and still be wrong for the third sensor,
-- the mapping moves onto the device: which IO ID carries fuel, and the linear
-- transform from its raw units to percent.
--
--     fuel_percent = raw_value * fuel_scale + fuel_offset      (clamped 0-100)
--
-- Worked examples:
--   CAN percent (IO 89, tenths)     io=89   scale=0.1        offset=0
--   LLS 0-4095 full range           io=201  scale=0.024420   offset=0
--   Analog 0.5-4.5 V sender (mV)    io=9    scale=0.025      offset=-12.5
--
-- The defaults reproduce today's behaviour exactly on every existing row, so
-- this migration changes no reading anywhere until a device is deliberately
-- reconfigured.

ALTER TABLE iot_devices
    ADD COLUMN IF NOT EXISTS fuel_io_id  INTEGER          NOT NULL DEFAULT 89,
    ADD COLUMN IF NOT EXISTS fuel_scale  DOUBLE PRECISION NOT NULL DEFAULT 0.1,
    ADD COLUMN IF NOT EXISTS fuel_offset DOUBLE PRECISION NOT NULL DEFAULT 0;

COMMENT ON COLUMN iot_devices.fuel_io_id IS
  'Protocol IO element carrying fuel level. 89 = Teltonika CAN percent (the default and previous hardcoded behaviour); 9 = AIN1 analog sender; 201/203 = LLS probe 1/2 on FMB firmware — confirm against the unit''s own IO list, numbering varies by model and CAN adapter. 0 disables fuel decoding for this device.';

COMMENT ON COLUMN iot_devices.fuel_scale IS
  'Multiplier from the raw IO value to percent. 0.1 for CAN tenths-of-a-percent; 100/full_scale for a raw-count probe (e.g. 0.024420 for 0-4095).';

COMMENT ON COLUMN iot_devices.fuel_offset IS
  'Added after scaling. Non-zero for senders whose usable range does not start at zero — a 0.5-4.5 V analog sender reads 500 mV at empty, so the offset cancels it.';

-- A negative scale would invert the reading, which silently turns every refuel
-- into a theft alert in the fuel-event detector. Zero is the same as disabling
-- the sensor, and there is already an explicit way to say that (fuel_io_id = 0)
-- that does not also hide the mistake behind a plausible-looking config.
ALTER TABLE iot_devices
    DROP CONSTRAINT IF EXISTS iot_devices_fuel_scale_positive;
ALTER TABLE iot_devices
    ADD CONSTRAINT iot_devices_fuel_scale_positive CHECK (fuel_scale > 0);

-- IO ids are unsigned 16-bit on the wire for every protocol here.
ALTER TABLE iot_devices
    DROP CONSTRAINT IF EXISTS iot_devices_fuel_io_id_range;
ALTER TABLE iot_devices
    ADD CONSTRAINT iot_devices_fuel_io_id_range CHECK (fuel_io_id BETWEEN 0 AND 65535);
