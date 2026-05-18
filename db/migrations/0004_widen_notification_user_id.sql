-- Widen notifications.user_id and notification_preferences.user_id from
-- INTEGER to BIGINT so they match users.id (BIGSERIAL).
--
-- Background: 0003 originally declared user_id as INTEGER. Postgres
-- accepts INTEGER → BIGINT FKs via implicit cast, so the schema worked,
-- but the cosmetic mismatch meant a hypothetical user table past 2^31
-- rows couldn't write a notification. Forward-only fix here instead of
-- editing 0003 (the runner records a checksum per applied migration and
-- refuses to proceed when an applied file's body changes; restoring 0003
-- to its original body is what unblocked the boot loop on Railway).
--
-- ALTER TABLE ... TYPE BIGINT on a column that holds INTEGER values is a
-- straight metadata + rewrite-each-row operation; on the small tables
-- this targets it's near-instant. Both clauses are idempotent on a
-- column that's already BIGINT (Postgres no-ops the type change).

ALTER TABLE notifications
    ALTER COLUMN user_id TYPE BIGINT;

ALTER TABLE notification_preferences
    ALTER COLUMN user_id TYPE BIGINT;
