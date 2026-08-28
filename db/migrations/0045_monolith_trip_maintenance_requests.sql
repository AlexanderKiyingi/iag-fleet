-- Trip and maintenance requests from the ERP monolith
--
-- Created during the ERP monolith clone, when the tail of the migration turned
-- out to have no platform tables to land in. Recorded here so a fresh
-- environment reproduces them; IF NOT EXISTS makes this a no-op where they
-- already stand.
--
-- Cross-service references (project_id -> finance.projects) are plain columns
-- rather than foreign keys: the services deploy independently and a constraint
-- would couple their migration order. The accompanying *_name column carries
-- the source's own text, so the link survives even where the id does not.

-- vehicle_ref and driver_ref are the source's free text, NOT foreign keys. One
-- fuel request already failed to resolve its vehicle ("FA-VAN-01"), and refusing
-- to record a trip because its vehicle string is unrecognised would lose the
-- trip. Resolve later; keep the record now.
CREATE TABLE IF NOT EXISTS public.trip_requests (
    id             UUID PRIMARY KEY,
    reference      TEXT NOT NULL UNIQUE,
    vehicle_ref    TEXT NOT NULL DEFAULT '',
    driver_ref     TEXT NOT NULL DEFAULT '',
    department     TEXT NOT NULL DEFAULT '',
    from_location  TEXT NOT NULL DEFAULT '',
    to_location    TEXT NOT NULL DEFAULT '',
    purpose        TEXT NOT NULL DEFAULT '',
    estimated_km   NUMERIC(12,2),
    depart_on      DATE,
    request_date   DATE,
    status         TEXT NOT NULL DEFAULT 'Pending',
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.maintenance_requests (
    id              UUID PRIMARY KEY,
    reference       TEXT NOT NULL UNIQUE,
    vehicle_ref     TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    requested_by    TEXT NOT NULL DEFAULT '',
    priority        TEXT NOT NULL DEFAULT '',
    estimated_cost  NUMERIC(18,4),
    amount          NUMERIC(18,4),
    currency        TEXT NOT NULL DEFAULT 'UGX',
    status          TEXT NOT NULL DEFAULT 'Pending',
    needed_by       DATE,
    request_date    DATE,
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
