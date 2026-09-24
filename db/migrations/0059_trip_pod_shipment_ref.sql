-- 0059: the last-mile shipment a POD closes
--
-- 0058 hung proof of delivery off a trip, which is right as far as it goes: a
-- POD is evidence for a vehicle movement. But on the Logistics desk a POD is
-- also the only honest close for a last-mile *shipment*, and shipments are not
-- in this database — no fleet resource owns a consignment (customer, sales
-- order, delivery note, packages, tracking code) yet.
--
-- With only trip_id on the row, that app had nowhere to record which shipment
-- was signed for. It therefore pointed its `shipment` field at trip_id, which
-- is both wrong and load-bearing: the field is what carrier scorecards, the
-- customer-facing /track/{code} page and the "Delivered needs a Confirmed Good
-- POD" gate all match on, so a POD could either save or close a shipment, but
-- never both.
--
-- Free text rather than a foreign key, deliberately: the collection it points
-- at lives in another service. The partial index keeps the empty default —
-- every POD recorded from Fleet itself — out of the index.

ALTER TABLE trip_pods ADD COLUMN IF NOT EXISTS shipment_ref TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS trip_pods_shipment_ref_idx
    ON trip_pods (shipment_ref)
    WHERE shipment_ref <> '';
