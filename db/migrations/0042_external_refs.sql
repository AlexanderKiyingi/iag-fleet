-- 0042: External reference map
--
-- Records which foreign record a fleet row came from, so an import can be re-run
-- without duplicating rows and a row can be traced back to its source. Warehouse
-- has carried this table since its receipts work; fleet had no equivalent.
--
-- target_id is TEXT, which is also fleet's own key type. It is a correlation key
-- rather than a foreign key, and the services this map spans do not agree on a
-- key type — procurement, CRM and fleet use text, finance and ERP use uuid,
-- quality control uses bigint. Text represents all of them without loss and
-- keeps one table shape across every service.
--
-- origin records which side last wrote the row: a relay importing from another
-- system stamps its own label, and a relay running the other direction skips
-- those rows. Without it two relays echo each other's writes indefinitely.
--
-- source_version is the source's own monotonic cursor for that record, so a
-- replayed or out-of-order change can be recognised as stale and dropped rather
-- than overwriting newer data.

CREATE TABLE IF NOT EXISTS fleet_external_refs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_service TEXT NOT NULL,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    target_type    TEXT NOT NULL,
    target_id      TEXT NOT NULL,
    origin         TEXT NOT NULL DEFAULT 'platform',
    source_version BIGINT NOT NULL DEFAULT 0,
    synced_at      TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_service, source_type, source_id)
);

CREATE INDEX IF NOT EXISTS idx_fleet_external_refs_target
    ON fleet_external_refs (target_type, target_id);

CREATE INDEX IF NOT EXISTS idx_fleet_external_refs_cursor
    ON fleet_external_refs (source_service, source_version DESC);
