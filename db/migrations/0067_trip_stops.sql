-- 0067: Planned / executed stop list on delivery trips.
ALTER TABLE trips ADD COLUMN IF NOT EXISTS stops JSONB NOT NULL DEFAULT '[]'::jsonb;
