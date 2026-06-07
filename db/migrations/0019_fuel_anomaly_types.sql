-- Multiple anomaly rule hits per fuel record (primary type remains anomaly_type).
ALTER TABLE fuel_records
    ADD COLUMN IF NOT EXISTS anomaly_types JSONB NOT NULL DEFAULT '[]'::jsonb;
