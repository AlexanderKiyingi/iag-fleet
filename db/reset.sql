-- HAULA Fleet · destructive reset.
-- Drops every table the seed script creates. Used by `cmd/seed --reset`.

BEGIN;

DROP TABLE IF EXISTS fuel_events            CASCADE;
DROP TABLE IF EXISTS telemetry_daily        CASCADE;
DROP TABLE IF EXISTS telemetry_pings        CASCADE;
DROP TABLE IF EXISTS iot_devices            CASCADE;
DROP TABLE IF EXISTS notifications            CASCADE;
DROP TABLE IF EXISTS notification_preferences CASCADE;
DROP TABLE IF EXISTS notification_recipients  CASCADE;
DROP TABLE IF EXISTS auth_tokens            CASCADE;
DROP TABLE IF EXISTS auth_sessions          CASCADE;
DROP TABLE IF EXISTS user_user_permissions  CASCADE;
DROP TABLE IF EXISTS group_permissions      CASCADE;
DROP TABLE IF EXISTS user_groups            CASCADE;
DROP TABLE IF EXISTS auth_permissions       CASCADE;
DROP TABLE IF EXISTS auth_groups            CASCADE;
DROP TABLE IF EXISTS users                  CASCADE;
DROP TABLE IF EXISTS audit_entries          CASCADE;
DROP TABLE IF EXISTS deployment_entries     CASCADE;
DROP TABLE IF EXISTS deployment_days      CASCADE;
DROP TABLE IF EXISTS task_items           CASCADE;
DROP TABLE IF EXISTS service_requests     CASCADE;
DROP TABLE IF EXISTS compliance_items     CASCADE;
DROP TABLE IF EXISTS safety_events        CASCADE;
DROP TABLE IF EXISTS trips                CASCADE;
DROP TABLE IF EXISTS tyres                CASCADE;
DROP TABLE IF EXISTS parts                CASCADE;
DROP TABLE IF EXISTS maintenance_items    CASCADE;
DROP TABLE IF EXISTS fuel_records         CASCADE;
DROP TABLE IF EXISTS cargo_docs           CASCADE;
DROP TABLE IF EXISTS cargo                CASCADE;
DROP TABLE IF EXISTS jmps                 CASCADE;
DROP TABLE IF EXISTS vehicles             CASCADE;
DROP TABLE IF EXISTS drivers              CASCADE;
DROP TABLE IF EXISTS operator_ticker      CASCADE;

COMMIT;
