-- Timescale hypertables should not rely on BIGSERIAL id (not unique across chunks).
-- Natural key for reads is (vehicle_id, ts).

ALTER TABLE IF EXISTS telemetry_timeseries DROP COLUMN IF EXISTS id;
