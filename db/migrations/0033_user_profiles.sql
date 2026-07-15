-- Fleet-scoped user profile store. GET /api/users/me previously returned only
-- JWT claims; there was nowhere to persist a user's phone/department/bio/avatar.
-- Keyed by the platform JWT subject (UUID text). One row per platform user,
-- lazily created on first PUT /api/users/me/profile. All fields default to ''
-- so a partially-filled profile round-trips cleanly through the string model.
CREATE TABLE IF NOT EXISTS user_profiles (
    user_id       TEXT PRIMARY KEY,
    display_name  TEXT NOT NULL DEFAULT '',
    role          TEXT NOT NULL DEFAULT '',
    department    TEXT NOT NULL DEFAULT '',
    phone         TEXT NOT NULL DEFAULT '',
    contact_email TEXT NOT NULL DEFAULT '',
    bio           TEXT NOT NULL DEFAULT '',
    avatar        TEXT NOT NULL DEFAULT '',
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
