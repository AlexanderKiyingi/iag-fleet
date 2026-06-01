-- Drop audit_entries.user_id (legacy BIGINT FK that pointed at the local
-- users table, dropped in 0007_drop_legacy_auth.sql during the platform
-- cutover). The column has been permanently NULL since that migration —
-- audit identity lives in the "user" snapshot text column. Repository.Log
-- (commit fad071d) no longer writes to it.
ALTER TABLE audit_entries DROP COLUMN IF EXISTS user_id;
