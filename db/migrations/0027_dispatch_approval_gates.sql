-- Independent per-gate approval trail for the dispatch chain. Each gate is
-- approved on its own (no enforced ordering) by its own role; these columns
-- record who approved each gate and when, mirroring the request approver
-- fields added in 0026 and the JMP mileage approver fields in 0001.

-- Vehicle + driver assignment approval (gate on the service request).
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS assignment_approved_by TEXT;
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS assignment_approved_at TIMESTAMPTZ;

-- Deployment = releasing the assigned vehicle/driver for the task/journey.
-- deployment_entry_id links the request to the row added to the daily sheet.
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS deployed_by TEXT;
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS deployed_at TIMESTAMPTZ;
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS deployment_entry_id TEXT;

-- Pre-dispatch (pre-trip) approval of the journey plan, distinct from the
-- existing post-trip mileage approval (jmps.mileage_status / approved_*).
-- Left nullable: historical JMPs stay out of the dispatch-approval queue;
-- new JMPs are stamped 'Pending' in code at creation.
ALTER TABLE jmps ADD COLUMN IF NOT EXISTS dispatch_status TEXT;
ALTER TABLE jmps ADD COLUMN IF NOT EXISTS dispatch_approved_by TEXT;
ALTER TABLE jmps ADD COLUMN IF NOT EXISTS dispatch_approved_at TIMESTAMPTZ;

-- Chain linkage: a fuel request can belong to a service request / journey plan.
ALTER TABLE fuel_requests ADD COLUMN IF NOT EXISTS request_id TEXT;
ALTER TABLE fuel_requests ADD COLUMN IF NOT EXISTS jmp_id TEXT;
CREATE INDEX IF NOT EXISTS fuel_requests_request_id_idx ON fuel_requests (request_id);
CREATE INDEX IF NOT EXISTS fuel_requests_jmp_id_idx ON fuel_requests (jmp_id);
