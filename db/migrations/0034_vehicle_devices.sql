-- Persist the per-vehicle IoT/accessory device list the frontend previously
-- kept in browser localStorage. PATCH /api/vehicles/:id already sends
-- {devices:[...]} but the column didn't exist so it was silently dropped.
-- JSONB (not a typed table) because the device objects carry arbitrary,
-- evolving fields (id, backendId, type, brand, serialNumber, label, ...).
-- Defaults to '[]' so existing rows echo an empty list, matching the model.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS devices JSONB NOT NULL DEFAULT '[]';
