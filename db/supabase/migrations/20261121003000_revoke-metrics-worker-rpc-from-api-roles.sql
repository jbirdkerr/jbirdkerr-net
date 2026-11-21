-- Only the metrics worker (service_role) may call the metrics_worker write functions.
--
-- Both functions were created in `public`, where Supabase's default privileges grant
-- EXECUTE on new functions to anon and authenticated explicitly. The original
-- migrations ran `REVOKE ALL ... FROM PUBLIC`, which does not remove those explicit
-- grants, and the grants moved with the functions into metrics_worker. Because both
-- functions are SECURITY DEFINER and the schema is exposed through PostgREST, anyone
-- with the project's anon key could call them to insert events or inflate counters.
--
-- service_role and postgres keep EXECUTE. Covered by
-- db/supabase/tests/metrics_worker_security.test.sql.

REVOKE EXECUTE ON FUNCTION metrics_worker.record_site_events(jsonb)
  FROM anon, authenticated;

REVOKE EXECUTE ON FUNCTION metrics_worker.record_worker_health(text, int, int, int, boolean, boolean, boolean)
  FROM anon, authenticated;
