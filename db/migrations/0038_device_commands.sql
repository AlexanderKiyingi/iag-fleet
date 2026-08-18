-- Server→device command queue, built for remote immobilisation.
--
-- The telemetry gateway has always been ingest-only: devices dial in, it reads.
-- Immobilisation needs the opposite direction, and the direction is the easy
-- half. Cutting the engine on a moving truck can kill someone, so the queue is
-- designed around refusing rather than around delivering:
--
--   * Interlocks are re-evaluated at DELIVERY, not at enqueue. A command may sit
--     queued for minutes while the vehicle pulls onto a highway, so the speed
--     check has to run against the state at the moment of sending. Checking only
--     at request time would be security theatre.
--   * Every attempt is recorded, including refused ones. "Why did nothing
--     happen" and "who tried to stop this truck" must both be answerable.
--   * Commands expire. A queued immobilise that lands six hours later, on a
--     vehicle nobody is watching, is worse than one that never lands.

CREATE TABLE IF NOT EXISTS device_commands (
    id             BIGSERIAL PRIMARY KEY,
    device_id      BIGINT NOT NULL REFERENCES iot_devices(id) ON DELETE CASCADE,
    vehicle_id     TEXT,
    kind           TEXT NOT NULL,                       -- immobilize | mobilize
    status         TEXT NOT NULL DEFAULT 'pending',     -- pending | sent | refused | expired | failed
    requested_by   TEXT NOT NULL,
    requested_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ NOT NULL,
    -- Vehicle state at the moment of the decision, so an incident review can
    -- reconstruct what the operator's action was based on.
    decided_at     TIMESTAMPTZ,
    decision_speed_kmh DOUBLE PRECISION,
    decision_fix_age_s INTEGER,
    refused_reason TEXT,
    sent_at        TIMESTAMPTZ,
    payload        TEXT,
    CONSTRAINT device_commands_kind CHECK (kind IN ('immobilize','mobilize')),
    CONSTRAINT device_commands_status CHECK (status IN ('pending','sent','refused','expired','failed'))
);

-- The delivery loop claims pending, unexpired commands for connected devices.
CREATE INDEX IF NOT EXISTS device_commands_pending_idx
    ON device_commands (device_id, status, expires_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS device_commands_audit_idx
    ON device_commands (vehicle_id, requested_at DESC);

-- At most one pending command per device: queueing three immobilises and
-- letting them all fire as the truck reconnects is not a behaviour anyone wants.
CREATE UNIQUE INDEX IF NOT EXISTS device_commands_one_pending_idx
    ON device_commands (device_id)
    WHERE status = 'pending';

COMMENT ON TABLE device_commands IS
  'Queue and audit trail for server-to-device commands. Refused attempts are retained deliberately.';
COMMENT ON COLUMN device_commands.decision_speed_kmh IS
  'Vehicle speed used by the interlock at delivery time, not at request time.';
COMMENT ON COLUMN device_commands.payload IS
  'Exact bytes sent, recorded for incident review. NULL when the command was never sent.';
