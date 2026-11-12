-- Rehearse the metrics-worker schema move against the live database without keeping any change.
--
-- Run from the repository root as the admin (postgres) user. ON_ERROR_STOP makes psql exit on the
-- first failure, and closing the connection rolls the open transaction back. The final ROLLBACK
-- covers the success path. Do not remove it.
--
--   psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 -f infra/appops/docs/phase6/rehearsal.sql
--
-- Expect every NOTICE below to start with "OK" and the two comparison columns to be t.

BEGIN;

\i db/supabase/migrations/20261111180000_move-metrics-worker-schema.sql

DO $$
DECLARE
  n int;
BEGIN
  -- 1. Nothing is left behind in public.
  SELECT count(*) INTO n FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
   WHERE s.nspname = 'public'
     AND c.relname IN ('site_events','site_event_counts','site_metrics','worker_health_stats','site_events_id_seq');
  IF n <> 0 THEN RAISE EXCEPTION 'FAIL: % metrics objects still in public', n; END IF;
  SELECT count(*) INTO n FROM pg_proc p JOIN pg_namespace s ON s.oid = p.pronamespace
   WHERE s.nspname = 'public' AND p.proname IN ('record_site_events','record_worker_health');
  IF n <> 0 THEN RAISE EXCEPTION 'FAIL: % functions still in public', n; END IF;

  -- 2. Everything arrived, including the identity sequence.
  SELECT count(*) INTO n FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
   WHERE s.nspname = 'metrics_worker'
     AND c.relname IN ('site_events','site_event_counts','site_metrics','worker_health_stats','site_events_id_seq');
  IF n <> 5 THEN RAISE EXCEPTION 'FAIL: expected 5 relations in metrics_worker, found %', n; END IF;
  RAISE NOTICE 'OK: objects moved (tables, sequence, functions)';

  -- 3. Realtime publication membership survived.
  SELECT count(*) INTO n FROM pg_publication_tables
   WHERE pubname = 'supabase_realtime' AND schemaname = 'metrics_worker' AND tablename = 'site_metrics';
  IF n <> 1 THEN RAISE EXCEPTION 'FAIL: site_metrics left the supabase_realtime publication'; END IF;
  RAISE NOTICE 'OK: site_metrics still published to supabase_realtime';

  -- 4. Function bodies no longer reference public.
  SELECT count(*) INTO n FROM pg_proc p JOIN pg_namespace s ON s.oid = p.pronamespace
   WHERE s.nspname = 'metrics_worker'
     AND p.proname IN ('record_site_events','record_worker_health')
     AND (p.prosrc LIKE '%public.%' OR 'search_path=metrics_worker' <> ALL (COALESCE(p.proconfig, '{}')));
  IF n <> 0 THEN RAISE EXCEPTION 'FAIL: % function(s) still reference public or have the wrong search_path', n; END IF;
  RAISE NOTICE 'OK: function bodies and search_path point at metrics_worker';
END $$;

-- 5. Behavior as the worker would call it (service_role). Writes are rolled back below.
SELECT total_events AS events_before FROM metrics_worker.site_metrics \gset
SELECT total_requests AS requests_before FROM metrics_worker.worker_health_stats \gset

SET LOCAL ROLE service_role;
SELECT metrics_worker.record_site_events('[{"event":"appops_rehearsal"}]'::jsonb) IS NOT NULL AS site_events_ok;
SELECT metrics_worker.record_worker_health('health', 200);
SELECT count(*) AS counts_visible FROM metrics_worker.site_event_counts;
RESET ROLE;

-- psql does not interpolate :variables inside DO blocks, so compare in plain SQL. Both must be t.
SELECT (SELECT total_events FROM metrics_worker.site_metrics) = :events_before + 1 AS site_events_incremented,
       (SELECT total_requests FROM metrics_worker.worker_health_stats) = :requests_before + 1 AS health_incremented;

-- 6. Public read policies still work for the anon role.
SET LOCAL ROLE anon;
SELECT count(*) AS anon_can_read_metrics FROM metrics_worker.site_metrics;
RESET ROLE;

ROLLBACK;
\echo 'Rolled back. No changes were kept.'
