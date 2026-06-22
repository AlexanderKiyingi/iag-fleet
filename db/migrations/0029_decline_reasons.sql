-- Decline-with-reason persistence for the JMP approval gates. Service requests
-- and fuel requests already record the decliner's reason in their existing
-- reviewer_notes column; the JMP dispatch/mileage gates previously kept the
-- reason in the audit log only, so add dedicated columns so the reason is
-- visible on the record itself. Nullable: only set when a gate is declined.
ALTER TABLE jmps ADD COLUMN IF NOT EXISTS dispatch_reject_reason TEXT;
ALTER TABLE jmps ADD COLUMN IF NOT EXISTS mileage_reject_reason TEXT;
