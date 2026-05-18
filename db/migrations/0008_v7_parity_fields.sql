-- v7 prototype parity: extended fuel, parts, compliance, and lifecycle histories.

ALTER TABLE fuel_records
    ADD COLUMN IF NOT EXISTS payment_method  TEXT,
    ADD COLUMN IF NOT EXISTS attendant       TEXT,
    ADD COLUMN IF NOT EXISTS card_last4      TEXT,
    ADD COLUMN IF NOT EXISTS anomaly_type    TEXT,
    ADD COLUMN IF NOT EXISTS anomaly_status  TEXT,
    ADD COLUMN IF NOT EXISTS anomaly_history JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE parts
    ADD COLUMN IF NOT EXISTS reorder_qty      INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS lead_time_days   INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_received    DATE,
    ADD COLUMN IF NOT EXISTS last_consumed    DATE;

ALTER TABLE compliance_items
    ADD COLUMN IF NOT EXISTS renewal_cost_ugx DOUBLE PRECISION;

ALTER TABLE maintenance_items
    ADD COLUMN IF NOT EXISTS status_history JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE safety_events
    ADD COLUMN IF NOT EXISTS status_history JSONB NOT NULL DEFAULT '[]'::jsonb;
