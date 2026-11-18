-- 0068: Link delivery trips to packing lists / distribution (SCM) orders.
-- Additive text refs — packing list is a warehouse UUID/id; sales_order_ref is
-- the SCM business id shown on Distribution Orders.

ALTER TABLE trips ADD COLUMN IF NOT EXISTS packing_list_id TEXT NOT NULL DEFAULT '';
ALTER TABLE trips ADD COLUMN IF NOT EXISTS sales_order_ref TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS trips_packing_list_id_idx ON trips (packing_list_id)
  WHERE packing_list_id <> '';
CREATE INDEX IF NOT EXISTS trips_sales_order_ref_idx ON trips (sales_order_ref)
  WHERE sales_order_ref <> '';
