-- Device type and brand, so hardware is chosen rather than spelled.
--
-- 0035 added `model` as free text to key the status-word bit map. It never got
-- a brand, so "ST-901" carried the brand implicitly and only for a reader who
-- recognised it, and nothing at all recorded whether a row was a GPS tracker or
-- a fuel probe. Two consequences:
--
--   * The fuel columns from 0047 (fuel_io_id / scale / offset) read as though
--     every device had a fuel sensor. A probe is not a device that dials in —
--     it is wired into a tracker's IO — so the distinction has to be recorded
--     to be honest about which rows those columns even apply to.
--
--   * Model matching is exact, so a brand's own naming variants ("ST 901",
--     "st901") silently disabled per-model decoding. Validating against a
--     catalogue at registration turns that into an error the operator sees.
--
-- Both default to '' rather than NOT NULL with a guess: existing rows genuinely
-- have unknown type and brand, and inventing one would be a claim the data does
-- not support. The catalogue treats empty as unknown, which is what it is.

ALTER TABLE iot_devices ADD COLUMN IF NOT EXISTS device_type TEXT NOT NULL DEFAULT '';
ALTER TABLE iot_devices ADD COLUMN IF NOT EXISTS brand       TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN iot_devices.device_type IS
  'gps_tracker | fuel_sensor. Empty means unknown (rows predating this column). A fuel_sensor has no network identity: it attaches to a tracker and reports through it.';
COMMENT ON COLUMN iot_devices.brand IS
  'Hardware brand, e.g. SinoTrack, Teltonika, Technoton. "Other" records hardware with no decoder. Empty means unknown.';

-- Brand + model is how the catalogue is looked up on every device read.
CREATE INDEX IF NOT EXISTS iot_devices_brand_model_idx ON iot_devices (brand, model);
