-- Server-side overspeed monitoring.
--
-- Speed arrives on every ping and nothing has ever looked at it beyond the
-- 5 km/h moving/idle threshold. Doing this server-side rather than via the
-- tracker's own overspeed alarm is deliberate: the device threshold is set once
-- over SMS per unit, is invisible to the platform, differs between models, and
-- on HQ hardware it rides in the same undecoded status word as everything else.
-- A server-side limit is per-vehicle, changeable without touching hardware, and
-- works identically for every protocol.

ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS speed_limit_kmh DOUBLE PRECISION;

COMMENT ON COLUMN vehicles.speed_limit_kmh IS
  'Per-vehicle speed limit. NULL falls back to FLEET_SPEED_LIMIT_KMH; 0 disables monitoring for this vehicle.';

-- One row per vehicle tracking whether it is currently over the limit.
--
-- Without this, every ping above the limit would raise an event and a single
-- highway run would bury the safety queue. Instead a breach is a state: it
-- opens, must be SUSTAINED before it alerts (a lone GPS speed spike is noise,
-- not speeding), alerts exactly once, and only re-arms after the vehicle drops
-- back below the limit by a margin — so hovering at 80.4 km/h cannot oscillate.
CREATE TABLE IF NOT EXISTS vehicle_overspeed_state (
    vehicle_id        TEXT PRIMARY KEY REFERENCES vehicles(id) ON DELETE CASCADE,
    breaching         BOOLEAN NOT NULL DEFAULT FALSE,
    breach_started_at TIMESTAMPTZ,
    peak_speed_kmh    DOUBLE PRECISION NOT NULL DEFAULT 0,
    alerted           BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE vehicle_overspeed_state IS
  'Hysteresis state for overspeed alerting: one safety event per sustained breach, not one per ping.';
COMMENT ON COLUMN vehicle_overspeed_state.alerted IS
  'True once this breach has raised its safety event, so a long breach does not alert repeatedly.';
COMMENT ON COLUMN vehicle_overspeed_state.peak_speed_kmh IS
  'Highest speed seen during the current breach; reported in the safety event.';
