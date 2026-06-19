BEGIN;

-- Poison outbox events (permanent dispatch failures) were retried forever with
-- capped backoff, never draining. Mark them dead after max attempts so the
-- claim scan skips them; ops can inspect/replay via dead_lettered_at + last_error.
ALTER TABLE fleet_event_outbox
    ADD COLUMN IF NOT EXISTS dead_lettered_at TIMESTAMPTZ;

-- Keep the due-claim index aligned with ClaimBatch's filter so dead-lettered
-- rows drop out of the partial index entirely.
DROP INDEX IF EXISTS fleet_event_outbox_due_idx;
CREATE INDEX IF NOT EXISTS fleet_event_outbox_due_idx
    ON fleet_event_outbox (available_at)
    WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL;

COMMIT;
