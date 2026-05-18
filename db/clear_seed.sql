-- HAULA Fleet · clear seeded business data
--
-- Wipes every fleet / ops / telemetry table while preserving:
--   * users               (demo + admin accounts, hashed passwords intact)
--   * auth_groups         (admin, fleet-manager, …)
--   * auth_permissions    (RBAC catalog)
--   * user_groups         (membership)
--   * group_permissions   (group → permission mappings)
--   * user_user_permissions (direct grants)
--   * auth_tokens, auth_sessions (active logins survive)
--   * schema_migrations   (migrate ledger)
--
-- After running this:
--   * Existing users can still sign in.
--   * Every fleet endpoint (/api/vehicles, /api/drivers, …) returns [].
--   * The Live Map / Command Center render empty until operators add data
--     via the UI or CSV import.
--   * Sequences are reset so newly-created rows start from 1 again.
--
-- Usage:
--   psql "$DATABASE_URL" -f db/clear_seed.sql
--   # or in Postgres CLI:
--   \i db/clear_seed.sql
--
-- After this you should NOT re-run `cmd/seed` (it would repopulate the
-- seeded business data via ON CONFLICT DO NOTHING). Use one of:
--   go run ./cmd/seed --schema-only   # apply migrations only
--   go run ./cmd/seed --reset         # full wipe + re-seed (the opposite of this script)
--
-- Idempotent: safe to run multiple times. Wrapped in a single transaction;
-- a failure rolls back so you never end up half-cleared.

BEGIN;

-- TRUNCATE … CASCADE handles inter-table FKs (e.g. maintenance_items.vehicle_id
-- → vehicles.id) automatically. RESTART IDENTITY resets every serial / bigserial
-- so newly-created rows start at 1 instead of continuing from the seeded high.
TRUNCATE TABLE
    -- Operations
    drivers,
    vehicles,
    jmps,
    cargo,
    cargo_docs,
    fuel_records,
    fuel_events,
    maintenance_items,
    parts,
    tyres,
    trips,
    safety_events,
    compliance_items,
    service_requests,
    task_items,
    deployment_days,
    -- Workspace constants (operator/role ticker row)
    operator_ticker,
    -- Activity log (seeded entries only — live entries restart from this point)
    audit_entries,
    -- IoT side
    iot_devices,
    telemetry_pings,
    telemetry_daily
RESTART IDENTITY CASCADE;

COMMIT;
