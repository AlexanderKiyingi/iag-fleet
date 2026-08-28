-- 0045: jmps.notes
--
-- Missed by 0044. The journey-plan form has carried a notes field all along and
-- the JMP model has no column for it, so every note an operator wrote against a
-- journey plan was discarded by the browser.
--
-- A separate file rather than an edit to 0044 because migrations are immutable
-- once applied and 0044 is already published -- amending it would break the
-- checksum for anyone who had run it, and a refused boot is a worse outcome
-- than an extra file.
--
-- Nullable with a default so the generic CRUD insert, which names every column,
-- writes '' rather than tripping the NOT NULL it would otherwise need.

ALTER TABLE jmps ADD COLUMN IF NOT EXISTS notes TEXT NOT NULL DEFAULT '';
