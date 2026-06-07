BEGIN;

CREATE TABLE IF NOT EXISTS fleet_event_outbox (
    id            BIGSERIAL PRIMARY KEY,
    event_type    TEXT NOT NULL,
    event_key     TEXT,
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    available_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    dispatched_at TIMESTAMPTZ,
    attempts      INT NOT NULL DEFAULT 0,
    last_error    TEXT
);

CREATE INDEX IF NOT EXISTS fleet_event_outbox_due_idx
    ON fleet_event_outbox (available_at)
    WHERE dispatched_at IS NULL;

COMMIT;
