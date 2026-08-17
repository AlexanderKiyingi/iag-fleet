-- Forensic queries for the fail-open RBAC exposure.
--
-- Background: StrictRBAC() keyed off ENVIRONMENT=production, which the Railway
-- runbook never told anyone to set. On an instance where it was unset,
-- auth.HasPerm granted every permission to any token presenting an empty
-- permissions array (see internal/auth/platform.go). Fixed by
-- config.HardenedRuntime(), which also keys off Railway's own variables and
-- GIN_MODE=release.
--
-- Run against the PRODUCTION fleet database, read-only:
--   psql "$DATABASE_URL" -f scripts/audit-failopen-exposure.sql
--
-- IMPORTANT LIMITATION: fleet_api_audit records who called what, but not the
-- permission set the token carried. These queries therefore cannot prove a
-- given request was allowed *because* of the fail-open path — they surface the
-- calls worth reviewing against who legitimately had access. Absence of hits in
-- Q1/Q2 is meaningful; presence in Q3/Q4 needs human judgement.

\echo '== Q1: demo-only endpoints. Any successful hit in production is a finding. =='
SELECT logged_at, method, path, status_code, user_name, client_ip
  FROM fleet_api_audit
 WHERE (path LIKE '%/admin/reset%' OR path LIKE '%simulate-tick%')
 ORDER BY logged_at DESC
 LIMIT 200;

\echo ''
\echo '== Q2: other privileged/destructive paths, successful calls only =='
SELECT logged_at, method, path, status_code, user_name, client_ip
  FROM fleet_api_audit
 WHERE status_code BETWEEN 200 AND 299
   AND (
        (method = 'DELETE' AND path LIKE '%/bulk%')
     OR path LIKE '%/admin/%'
     OR path LIKE '%/iot/devices%'
   )
 ORDER BY logged_at DESC
 LIMIT 500;

\echo ''
\echo '== Q3: every principal that made a successful mutating call, with volume =='
\echo '   Cross-check this list against who was actually provisioned for fleet.'
\echo '   A principal here that no one recognises is the signal to chase.'
SELECT user_name,
       COUNT(*)                                   AS mutating_calls,
       COUNT(DISTINCT client_ip)                  AS distinct_ips,
       MIN(logged_at)                             AS first_seen,
       MAX(logged_at)                             AS last_seen
  FROM fleet_api_audit
 WHERE method IN ('POST', 'PUT', 'PATCH', 'DELETE')
   AND status_code BETWEEN 200 AND 299
 GROUP BY user_name
 ORDER BY mutating_calls DESC;

\echo ''
\echo '== Q4: principals whose calls were NEVER once refused =='
\echo '   Under fail-open a permissionless token is never told no. A principal'
\echo '   with meaningful volume and zero 401/403 is the shape to look for —'
\echo '   though a correctly-permissioned service account looks the same, so'
\echo '   this narrows the list rather than proving anything.'
SELECT user_name,
       COUNT(*) AS total_calls,
       MIN(logged_at) AS first_seen,
       MAX(logged_at) AS last_seen
  FROM fleet_api_audit
 GROUP BY user_name
HAVING SUM(CASE WHEN status_code IN (401, 403) THEN 1 ELSE 0 END) = 0
   AND COUNT(*) > 20
 ORDER BY total_calls DESC;

\echo ''
\echo '== Q5: audit coverage — how far back the trail actually goes =='
\echo '   If this starts later than the deployment, earlier activity is simply'
\echo '   not recorded and the queries above cannot speak to it.'
SELECT MIN(logged_at) AS earliest_entry,
       MAX(logged_at) AS latest_entry,
       COUNT(*)       AS total_entries
  FROM fleet_api_audit;

\echo ''
\echo '== Q6: destructive domain actions from the business audit trail =='
SELECT ts, action, entity, ref_id, "user", details
  FROM audit_entries
 WHERE action ILIKE '%reset%'
    OR action ILIKE '%delete%'
    OR action ILIKE '%bulk%'
 ORDER BY ts DESC
 LIMIT 300;
