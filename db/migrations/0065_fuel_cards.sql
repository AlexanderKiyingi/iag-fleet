-- Fuel cards V1: card registry, pump transactions, and links on fuel requests/logs.
--
-- Cards are assigned to exactly one of vehicle or driver while active.
-- Transactions are typed or CSV-imported; matching against requests/logs is
-- handled in the API (not geofences / not statement-balance reconciliations).

CREATE TABLE IF NOT EXISTS fuel_cards (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    card_number           TEXT NOT NULL,
    last4                 TEXT NOT NULL DEFAULT '',
    provider              TEXT NOT NULL DEFAULT '',
    label                 TEXT NOT NULL DEFAULT '',
    currency              TEXT NOT NULL DEFAULT 'UGX',
    vehicle_id            UUID REFERENCES vehicles(id) ON DELETE SET NULL,
    driver_id             UUID REFERENCES drivers(id)  ON DELETE SET NULL,
    daily_limit_litres    DOUBLE PRECISION,
    monthly_limit_amount  DOUBLE PRECISION,
    status                TEXT NOT NULL DEFAULT 'active',
    expires_on            DATE,
    notes                 TEXT NOT NULL DEFAULT '',
    CONSTRAINT fuel_cards_card_number_nonempty CHECK (length(trim(card_number)) > 0),
    CONSTRAINT fuel_cards_status_known CHECK (
        status IN ('active', 'blocked', 'expired', 'cancelled')
    ),
    CONSTRAINT fuel_cards_active_assignee CHECK (
        status <> 'active'
        OR (
            (vehicle_id IS NOT NULL AND driver_id IS NULL)
            OR (vehicle_id IS NULL AND driver_id IS NOT NULL)
        )
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS fuel_cards_card_number_uidx
    ON fuel_cards (lower(trim(card_number)));
CREATE INDEX IF NOT EXISTS fuel_cards_vehicle_idx ON fuel_cards (vehicle_id);
CREATE INDEX IF NOT EXISTS fuel_cards_driver_idx ON fuel_cards (driver_id);
CREATE INDEX IF NOT EXISTS fuel_cards_status_idx ON fuel_cards (status);

ALTER TABLE fuel_cards ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE fuel_cards ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
DROP TRIGGER IF EXISTS fuel_cards_touch ON fuel_cards;
CREATE TRIGGER fuel_cards_touch
    BEFORE INSERT OR UPDATE ON fuel_cards
    FOR EACH ROW EXECUTE FUNCTION touch_row();
CREATE INDEX IF NOT EXISTS fuel_cards_updated_at_idx ON fuel_cards (updated_at DESC);

COMMENT ON TABLE fuel_cards IS
  'Fleet fuel card registry. Active cards belong to exactly one vehicle or one driver.';

CREATE TABLE IF NOT EXISTS fuel_card_transactions (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fuel_card_id              UUID NOT NULL REFERENCES fuel_cards(id) ON DELETE RESTRICT,
    date                      DATE NOT NULL,
    litres                    DOUBLE PRECISION NOT NULL DEFAULT 0,
    amount                    DOUBLE PRECISION NOT NULL DEFAULT 0,
    unit_price                DOUBLE PRECISION NOT NULL DEFAULT 0,
    station                   TEXT NOT NULL DEFAULT '',
    merchant                  TEXT NOT NULL DEFAULT '',
    external_ref              TEXT NOT NULL DEFAULT '',
    match_status              TEXT NOT NULL DEFAULT 'unmatched',
    matched_fuel_request_id   UUID REFERENCES fuel_requests(id) ON DELETE SET NULL,
    matched_fuel_record_id    UUID REFERENCES fuel_records(id)  ON DELETE SET NULL,
    import_batch_id           TEXT NOT NULL DEFAULT '',
    notes                     TEXT NOT NULL DEFAULT '',
    CONSTRAINT fuel_card_transactions_litres_nonneg CHECK (litres >= 0),
    CONSTRAINT fuel_card_transactions_amount_nonneg CHECK (amount >= 0),
    CONSTRAINT fuel_card_transactions_match_known CHECK (
        match_status IN ('unmatched', 'matched', 'unfulfilled_request', 'ignored')
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS fuel_card_transactions_external_ref_uidx
    ON fuel_card_transactions (external_ref)
    WHERE external_ref <> '';
CREATE INDEX IF NOT EXISTS fuel_card_transactions_card_date_idx
    ON fuel_card_transactions (fuel_card_id, date DESC);
CREATE INDEX IF NOT EXISTS fuel_card_transactions_match_idx
    ON fuel_card_transactions (match_status);

ALTER TABLE fuel_card_transactions ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE fuel_card_transactions ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
DROP TRIGGER IF EXISTS fuel_card_transactions_touch ON fuel_card_transactions;
CREATE TRIGGER fuel_card_transactions_touch
    BEFORE INSERT OR UPDATE ON fuel_card_transactions
    FOR EACH ROW EXECUTE FUNCTION touch_row();
CREATE INDEX IF NOT EXISTS fuel_card_transactions_updated_at_idx
    ON fuel_card_transactions (updated_at DESC);

COMMENT ON TABLE fuel_card_transactions IS
  'Pump fills from a fuel card (typed or imported). Matched to requests/logs in the API.';

ALTER TABLE fuel_requests
    ADD COLUMN IF NOT EXISTS fuel_card_id UUID REFERENCES fuel_cards(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS fuel_requests_fuel_card_idx ON fuel_requests (fuel_card_id);

ALTER TABLE fuel_records
    ADD COLUMN IF NOT EXISTS fuel_card_id UUID REFERENCES fuel_cards(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS fuel_records_fuel_card_idx ON fuel_records (fuel_card_id);
