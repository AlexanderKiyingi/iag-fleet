-- Inspection templates can target a specific vehicle class (e.g. "Tipper
-- truck") so the DVIR UI can label and scope templates by vehicle type. The
-- frontend already reads and displays template.vehicleType (inspection list +
-- detail + CSV export), but the column was missing — so the value the
-- create/edit form collected was silently dropped on write. Nullable: existing
-- templates stay unscoped ("general").
ALTER TABLE inspection_templates ADD COLUMN IF NOT EXISTS vehicle_type TEXT;
