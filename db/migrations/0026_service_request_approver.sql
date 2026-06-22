-- Service-request approver audit trail. Until now the only trace of an
-- approval was the optional reviewer_notes free-text field — there was no
-- record of WHO approved a request or WHEN. These columns mirror the JMP
-- mileage-approval pattern (jmps.approved_by / jmps.approved_at) and are
-- stamped on the first transition into status='approved' by both the
-- /requests/:id/advance workflow endpoint and the generic PATCH path.
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS approved_by TEXT;
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ;
