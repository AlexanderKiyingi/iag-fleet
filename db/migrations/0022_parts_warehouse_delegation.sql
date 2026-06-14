-- Warehouse ("stores") delegation: iag-warehouse becomes the system-of-record
-- for spare-parts stock. Under delegation, parts.stock / parts.movements are no
-- longer authoritative — they become a read projection fed by warehouse events
-- (warehouse.issue.posted, warehouse.movement.posted, warehouse.stock.*). The
-- catalog fields fleet legitimately owns (name, category, sku, reorder_point,
-- vendor, lead_time_days, location) stay local.
--
-- warehouse_item_id maps a fleet part to its warehouse wh_items UUID. It is
-- populated by the reconciliation job (cmd/reconcile-warehouse) matching on SKU;
-- the issue path falls back to a live SKU lookup when it is NULL.
ALTER TABLE parts ADD COLUMN IF NOT EXISTS warehouse_item_id TEXT;

-- Last time this part's projected stock was reconciled from a warehouse event.
-- NULL = never synced (still showing legacy local stock).
ALTER TABLE parts ADD COLUMN IF NOT EXISTS warehouse_synced_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS parts_warehouse_item_id_idx
    ON parts (warehouse_item_id)
    WHERE warehouse_item_id IS NOT NULL;

-- Dedupe ledger for the inbound warehouse-event consumer. The projection
-- refresh is idempotent (it sets stock to the authoritative on-hand rather than
-- applying a delta), so this is a belt-and-suspenders guard plus an audit trail
-- of which warehouse events fleet has processed.
CREATE TABLE IF NOT EXISTS warehouse_event_dedupe (
    event_id TEXT PRIMARY KEY,
    topic    TEXT,
    seen_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
