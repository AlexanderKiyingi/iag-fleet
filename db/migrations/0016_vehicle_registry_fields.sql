-- Vehicle registry metadata (static asset details distinct from live telemetry odo).

ALTER TABLE vehicles
    ADD COLUMN IF NOT EXISTS vin              TEXT,
    ADD COLUMN IF NOT EXISTS color            TEXT,
    ADD COLUMN IF NOT EXISTS seat_capacity    INTEGER,
    ADD COLUMN IF NOT EXISTS transmission     TEXT,
    ADD COLUMN IF NOT EXISTS engine_capacity  TEXT,
    ADD COLUMN IF NOT EXISTS drive_hand       TEXT,
    ADD COLUMN IF NOT EXISTS purchase_date    DATE,
    ADD COLUMN IF NOT EXISTS mileage          DOUBLE PRECISION;

-- One VIN per vehicle when provided (multiple NULL/blank rows allowed).
CREATE UNIQUE INDEX IF NOT EXISTS vehicles_vin_uidx
    ON vehicles (vin)
    WHERE vin IS NOT NULL AND trim(vin) <> '';
