-- Device model/protocol, and a place to accumulate raw status words.
--
-- Two related problems this solves.
--
-- 1. Model is unknown. Every SinoTrack-family unit is provisioned identically
--    (serial + vehicle), so nothing records whether a device is an ST-901, an
--    ST-03, or a GT06-era clone. The HQ status/alarm bitfield layout varies
--    across those firmwares, so decoding ACC and alarms safely requires knowing
--    which model sent the frame — an inverted ACC bit would report every parked
--    truck as running.
--
-- 2. Status words are only kept for pings we insert. The gateway writes
--    hqStatus into telemetry_timeseries.raw, but frames it deliberately skips
--    (heartbeats, no-fix, 0,0, unbound devices) drop theirs. That is exactly the
--    set that matters: power-cut and tamper alarms routinely arrive without a
--    usable GPS fix, so a pilot could run for weeks and never capture the
--    frames needed to derive the bit layout.

ALTER TABLE iot_devices ADD COLUMN IF NOT EXISTS model    TEXT NOT NULL DEFAULT '';
ALTER TABLE iot_devices ADD COLUMN IF NOT EXISTS protocol TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN iot_devices.model IS
  'Hardware model, e.g. ST-901, ST-901L, ST-03. Keys the status-word bit map; empty means unknown and the word is stored raw without decoding.';
COMMENT ON COLUMN iot_devices.protocol IS
  'Wire protocol last observed from this device: hq | gt06 | teltonika | http. Empty until the first connection.';

-- Distinct (device, frame type, status word) with a count, rather than a row
-- per frame. A tracker repeats the same word on every report, so the raw stream
-- is enormous and almost entirely redundant; the distinct set is small, bounded
-- by the device's actual state space, and is precisely what you correlate
-- against observed physical state to derive the bit layout.
CREATE TABLE IF NOT EXISTS iot_status_observations (
    id            BIGSERIAL PRIMARY KEY,
    device_id     BIGINT NOT NULL REFERENCES iot_devices(id) ON DELETE CASCADE,
    frame_type    TEXT NOT NULL,
    status_word   TEXT NOT NULL,
    observations  BIGINT NOT NULL DEFAULT 1,
    had_fix       BOOLEAN NOT NULL DEFAULT FALSE,
    sample_frame  TEXT NOT NULL DEFAULT '',
    first_seen    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (device_id, frame_type, status_word)
);

CREATE INDEX IF NOT EXISTS iot_status_obs_device_idx ON iot_status_observations (device_id, last_seen DESC);

COMMENT ON TABLE iot_status_observations IS
  'Distinct status/alarm words seen per device and frame type, with a count. Feeds the per-model bit-map derivation; not a telemetry stream.';
COMMENT ON COLUMN iot_status_observations.had_fix IS
  'True when at least one frame carrying this word also had a valid GPS fix. Alarm-only frames typically do not.';
COMMENT ON COLUMN iot_status_observations.sample_frame IS
  'One representative raw frame body, kept for offline decoding. Bounded by the unique constraint, so this cannot grow per-ping.';
