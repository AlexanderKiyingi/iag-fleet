-- 0058: proof of delivery, on the trip
--
-- The Logistics app's Proof of Delivery screen (received by, phone,
-- condition, photos) had no owner. It is delivery evidence for a vehicle
-- movement, so it hangs off a trip: a POD cannot exist without the journey
-- it proves, and recording one is what moves the trip to completed. A row
-- rather than columns on trips because a trip with several drops can carry
-- several PODs, and because the evidence outlives edits to the trip.
--
-- trip_id is a real foreign key, RESTRICT on delete: the delete verb exists
-- on trips through the generic resource, and a signed-for delivery should
-- stop it rather than vanish with it. Photos are DMS attachment ids, as
-- everywhere else on the platform.

CREATE TABLE IF NOT EXISTS trip_pods (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id        UUID NOT NULL REFERENCES trips(id) ON DELETE RESTRICT,
    reference      TEXT NOT NULL DEFAULT '',
    date           DATE,
    customer       TEXT NOT NULL DEFAULT '',
    received_by    TEXT NOT NULL DEFAULT '',
    receiver_phone TEXT NOT NULL DEFAULT '',
    condition      TEXT NOT NULL DEFAULT 'good',
    photo_ids      JSONB NOT NULL DEFAULT '[]',
    status         TEXT NOT NULL DEFAULT 'delivered',
    notes          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS trip_pods_trip_idx ON trip_pods (trip_id);

DO $$
BEGIN
    ALTER TABLE trip_pods ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
    ALTER TABLE trip_pods ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
    DROP TRIGGER IF EXISTS trip_pods_touch ON trip_pods;
    CREATE TRIGGER trip_pods_touch BEFORE INSERT OR UPDATE ON trip_pods
        FOR EACH ROW EXECUTE FUNCTION touch_row();
    CREATE INDEX IF NOT EXISTS trip_pods_updated_at_idx ON trip_pods (updated_at DESC);
END;
$$;
