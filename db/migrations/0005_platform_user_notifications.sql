-- Platform IAM: notifications keyed by authentication user UUID (TEXT), not local users.id.
-- Drops FK to users so fleet can run with AUTH_MODE=gateway without the legacy auth schema.

ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_user_id_fkey;
ALTER TABLE notification_preferences DROP CONSTRAINT IF EXISTS notification_preferences_user_id_fkey;

ALTER TABLE notifications
    ALTER COLUMN user_id TYPE TEXT USING user_id::TEXT;

ALTER TABLE notification_preferences
    ALTER COLUMN user_id TYPE TEXT USING user_id::TEXT;

-- Users who have opened the fleet app (platform /users/me) receive in-app bell fan-out.
CREATE TABLE IF NOT EXISTS notification_recipients (
    user_id       TEXT PRIMARY KEY,
    email         TEXT NOT NULL DEFAULT '',
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS notification_recipients_registered_idx
    ON notification_recipients (registered_at DESC);
