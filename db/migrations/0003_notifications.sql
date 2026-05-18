-- Notifications + per-user preferences.
--
-- Each row is a per-user signal: the same underlying event (e.g. a single
-- expired permit) produces N rows, one per active user that should see it.
-- The unique constraint on (user_id, kind, ref_type, ref_id) is what makes
-- the producer idempotent — re-running the scanner inserts nothing new
-- until the source row's state changes (e.g. permit renewed → producer
-- skips → permit expires again later → new ref_id or kind transition).
--
-- seen_at and dismissed_at are user-scoped tombstones; the bell shows rows
-- where both are NULL.

CREATE TABLE IF NOT EXISTS notifications (
    id            TEXT PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,           -- compliance_expired | safety_crit | fuel_anomaly | parts_stockout | …
    ref_type      TEXT NOT NULL,           -- compliance | safety | fuel | parts | requests | cargo
    ref_id        TEXT NOT NULL,
    severity      TEXT NOT NULL CHECK (severity IN ('crit','warn','info')),
    title         TEXT NOT NULL,
    body          TEXT NOT NULL DEFAULT '',
    href          TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    seen_at       TIMESTAMPTZ,
    dismissed_at  TIMESTAMPTZ,
    UNIQUE (user_id, kind, ref_type, ref_id)
);

-- Hot path: bell badge counts unread per user. Partial index keeps this
-- tiny — once a notification is seen or dismissed it leaves the index.
CREATE INDEX IF NOT EXISTS notifications_unread_idx
    ON notifications (user_id, created_at DESC)
    WHERE dismissed_at IS NULL AND seen_at IS NULL;

-- Listing path: the bell pulls the latest visible (non-dismissed) rows.
CREATE INDEX IF NOT EXISTS notifications_visible_idx
    ON notifications (user_id, created_at DESC)
    WHERE dismissed_at IS NULL;

-- Per-user mute list: each entry is a `kind` the user doesn't want
-- generated. Stored as TEXT[] to keep schema flat — the set is small (a
-- handful of kinds) and updates atomically per user.
CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id     INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    muted_kinds TEXT[] NOT NULL DEFAULT '{}',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Permission codenames matching the existing seed convention. `entity`
-- is `notification`; the standard view/change verbs are enough — no
-- "delete" because the user-facing verb is "dismiss" (which is just a
-- timestamped column update gated by view + ownership).
INSERT INTO auth_permissions (codename, name, entity) VALUES
    ('view_notification',   'View notifications',         'notification'),
    ('change_notification', 'Mark / dismiss notifications','notification')
ON CONFLICT (codename) DO NOTHING;

-- Hand the new permissions to every existing group except viewer-only
-- needs view but not change. Mirrors the seed.sql pattern of bulk-grant
-- by codename pattern so re-running this migration on a populated DB
-- stays idempotent.
INSERT INTO group_permissions (group_id, permission_id)
SELECT g.id, p.id
FROM auth_groups g
JOIN auth_permissions p ON p.codename IN ('view_notification', 'change_notification')
WHERE g.name IN ('admin', 'fleet-manager', 'dispatcher')
ON CONFLICT DO NOTHING;

INSERT INTO group_permissions (group_id, permission_id)
SELECT g.id, p.id
FROM auth_groups g
JOIN auth_permissions p ON p.codename = 'view_notification'
WHERE g.name = 'viewer'
ON CONFLICT DO NOTHING;
