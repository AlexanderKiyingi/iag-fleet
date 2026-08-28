-- 0044: Give the fleet forms somewhere to put what they already collect
--
-- An audit of every mapped entity compared each form field against this
-- service's own model. A number of fields had no column here at all, so the
-- adapter dropped them on the way out: an operator typed an insurance policy
-- number, a driver's national ID or a trip's opening odometer, saved, and the
-- value never left the browser. The form asked, the service had nowhere to put
-- it, and nothing said so.
--
-- These are the fields worth keeping. Three groups were deliberately NOT added:
--
--   * expense_account / bank_account on fuel and maintenance. Fleet does not
--     post its own GL -- procurement -> finance is the only cost path -- so a
--     ledger account on a fleet row would re-open a settled decision, not close
--     a gap. The form fields are finance-clone inheritance and should go.
--   * currency. Every amount in this service is UGX and nothing reads a
--     currency; a column no writer sets is worse than an absent one.
--   * attachments. Documents belong to the DMS and need object storage, not a
--     text column pretending to hold a file.
--
-- Every column is additive, nullable or defaulted, and guarded with IF NOT
-- EXISTS, so this applies to a populated database without touching a row.
--
-- DATE columns are deliberately nullable rather than NOT NULL DEFAULT: the
-- generic CRUD resource inserts every column, and an empty string cast to date
-- writes NULL. A NOT NULL date with no value to supply would make creation
-- impossible -- the same failure 0040 fixed for vehicles.last_seen.

-- ─────────────────────────── Vehicles ──────────────────────────────────────
-- Insurance and registration also live on compliance_items, which is the right
-- home for the renewal workflow (ComplianceDocTypes carries "PSV Insurance" and
-- "Annual Inspection"). These columns are the vehicle's own current cover, read
-- by fleet-service-reminders to decide what is expiring; they are a snapshot on
-- the asset, not a replacement for the compliance record.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS insurance_provider  TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS insurance_policy    TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS insurance_expiry    DATE;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS registration_expiry DATE;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS next_service_date   DATE;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS notes               TEXT NOT NULL DEFAULT '';

-- Service reminders scan these two, so an index earns its keep once the fleet
-- is larger than a page.
CREATE INDEX IF NOT EXISTS vehicles_insurance_expiry_idx    ON vehicles (insurance_expiry);
CREATE INDEX IF NOT EXISTS vehicles_registration_expiry_idx ON vehicles (registration_expiry);

-- ─────────────────────────── Drivers ───────────────────────────────────────
-- permit_issue_date pairs with the permit_expiry this table has carried since
-- 0001: an expiry alone cannot tell you whether a permit was renewed or first
-- issued, which the authorisation matrix needs to reason about seniority.
ALTER TABLE drivers ADD COLUMN IF NOT EXISTS national_id       TEXT NOT NULL DEFAULT '';
ALTER TABLE drivers ADD COLUMN IF NOT EXISTS employee_no       TEXT NOT NULL DEFAULT '';
ALTER TABLE drivers ADD COLUMN IF NOT EXISTS hire_date         DATE;
ALTER TABLE drivers ADD COLUMN IF NOT EXISTS permit_issue_date DATE;

-- ─────────────────────────── Trips ─────────────────────────────────────────
-- distance_km already exists and is what the app computes from these two, but
-- storing only the difference loses the readings themselves -- which are what a
-- dispute over a trip is actually settled with.
ALTER TABLE trips ADD COLUMN IF NOT EXISTS purpose        TEXT NOT NULL DEFAULT '';
ALTER TABLE trips ADD COLUMN IF NOT EXISTS department     TEXT NOT NULL DEFAULT '';
ALTER TABLE trips ADD COLUMN IF NOT EXISTS odometer_start DOUBLE PRECISION;
ALTER TABLE trips ADD COLUMN IF NOT EXISTS odometer_end   DOUBLE PRECISION;

-- ─────────────────── Maintenance and fuel requests ─────────────────────────
-- `cost` on maintenance_items is what the work actually cost. An estimate is a
-- different number with a different purpose -- it is what the approval was
-- given against -- and overwriting one with the other loses the variance.
ALTER TABLE maintenance_items ADD COLUMN IF NOT EXISTS est_cost  DOUBLE PRECISION;
ALTER TABLE maintenance_items ADD COLUMN IF NOT EXISTS needed_by DATE;
ALTER TABLE fuel_requests     ADD COLUMN IF NOT EXISTS needed_by DATE;
