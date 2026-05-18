-- 0002 — JSONB ledgers for the three v7-parity workflows that the
-- initial schema deferred:
--
--   parts.movements           — stock ledger (in / out / adjust); the
--                               POST /api/parts/:id/movements endpoint
--                               appends a row and updates parts.stock
--                               in one transaction so the on-hand
--                               number and the audit trail can never
--                               disagree.
--
--   compliance_items.renewal_history
--                             — every time a doc is renewed the prior
--                               period (doc_number / issuer / issued /
--                               expiry / cost / note) is pushed onto
--                               this array; the row's top-level fields
--                               carry the *current* period only.
--
--   maintenance_items.parts_breakdown
--                             — line items referenced when a WO is
--                               completed. Each row is { partId, qty,
--                               unitCost, note? }; on transition to
--                               completed the workshop endpoint
--                               decrements parts.stock + appends an
--                               "out" movement per line in the same
--                               transaction.
--
-- All three default to '[]' so existing rows scan as empty slices
-- without backfill (the schema_migrations bookkeeping picks them up
-- on the next API boot).

ALTER TABLE parts
    ADD COLUMN IF NOT EXISTS movements JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE compliance_items
    ADD COLUMN IF NOT EXISTS renewal_history JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE maintenance_items
    ADD COLUMN IF NOT EXISTS parts_breakdown JSONB NOT NULL DEFAULT '[]'::jsonb;
