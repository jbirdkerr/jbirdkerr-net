-- Read functions the metrics worker calls for /metrics and /health-metrics: only
-- service_role (the worker) may call them, they run as their owner with a pinned
-- search_path, and they return the dashboard rows. Runs in a transaction that is
-- rolled back.
BEGIN;
CREATE EXTENSION IF NOT EXISTS pgtap WITH SCHEMA extensions;
SET search_path TO extensions, public;

SELECT plan(19);

-- Privileges
SELECT function_privs_are('metrics_worker', 'get_site_metrics', ARRAY[]::text[], 'anon', ARRAY[]::text[],
  'anon cannot call get_site_metrics');
SELECT function_privs_are('metrics_worker', 'get_site_metrics', ARRAY[]::text[], 'authenticated', ARRAY[]::text[],
  'authenticated cannot call get_site_metrics');
SELECT function_privs_are('metrics_worker', 'get_site_metrics', ARRAY[]::text[], 'service_role', ARRAY['EXECUTE'],
  'service_role can call get_site_metrics');

SELECT function_privs_are('metrics_worker', 'get_site_event_counts', ARRAY['integer'], 'anon', ARRAY[]::text[],
  'anon cannot call get_site_event_counts');
SELECT function_privs_are('metrics_worker', 'get_site_event_counts', ARRAY['integer'], 'authenticated', ARRAY[]::text[],
  'authenticated cannot call get_site_event_counts');
SELECT function_privs_are('metrics_worker', 'get_site_event_counts', ARRAY['integer'], 'service_role', ARRAY['EXECUTE'],
  'service_role can call get_site_event_counts');

SELECT function_privs_are('metrics_worker', 'get_worker_health_stats', ARRAY[]::text[], 'anon', ARRAY[]::text[],
  'anon cannot call get_worker_health_stats');
SELECT function_privs_are('metrics_worker', 'get_worker_health_stats', ARRAY[]::text[], 'authenticated', ARRAY[]::text[],
  'authenticated cannot call get_worker_health_stats');
SELECT function_privs_are('metrics_worker', 'get_worker_health_stats', ARRAY[]::text[], 'service_role', ARRAY['EXECUTE'],
  'service_role can call get_worker_health_stats');

-- Definer and search_path
SELECT is_definer('metrics_worker', 'get_site_metrics', ARRAY[]::text[], 'get_site_metrics runs as its owner');
SELECT is_definer('metrics_worker', 'get_site_event_counts', ARRAY['integer'], 'get_site_event_counts runs as its owner');
SELECT is_definer('metrics_worker', 'get_worker_health_stats', ARRAY[]::text[], 'get_worker_health_stats runs as its owner');
SELECT is(
  (SELECT array_agg(DISTINCT cfg) FROM pg_proc p, unnest(p.proconfig) AS cfg
    WHERE p.oid IN ('metrics_worker.get_site_metrics()'::regprocedure,
                    'metrics_worker.get_site_event_counts(int)'::regprocedure,
                    'metrics_worker.get_worker_health_stats()'::regprocedure)),
  ARRAY['search_path=metrics_worker'],
  'every read function pins search_path to metrics_worker');

-- Results
INSERT INTO metrics_worker.site_metrics (id) VALUES (true) ON CONFLICT (id) DO NOTHING;
INSERT INTO metrics_worker.worker_health_stats (id) VALUES (true) ON CONFLICT (id) DO NOTHING;
DELETE FROM metrics_worker.site_event_counts;
INSERT INTO metrics_worker.site_event_counts (event, count)
  SELECT 'read_test_' || i, i FROM generate_series(1, 120) AS i;

SET LOCAL ROLE service_role;

SELECT is((SELECT count(*) FROM metrics_worker.get_site_metrics()), 1::bigint,
  'get_site_metrics returns the summary row');
SELECT is((SELECT count(*) FROM metrics_worker.get_worker_health_stats()), 1::bigint,
  'get_worker_health_stats returns the stats row');
SELECT is((SELECT count(*) FROM metrics_worker.get_site_event_counts()), 30::bigint,
  'get_site_event_counts defaults to 30 rows');
SELECT is((SELECT count(*) FROM metrics_worker.get_site_event_counts(1000)), 100::bigint,
  'get_site_event_counts caps the limit at 100');
SELECT is((SELECT count(*) FROM metrics_worker.get_site_event_counts(-5)), 0::bigint,
  'get_site_event_counts treats a negative limit as 0');
SELECT is((SELECT event FROM metrics_worker.get_site_event_counts(1)), 'read_test_120',
  'get_site_event_counts orders by count, highest first');

RESET ROLE;

SELECT * FROM finish();
ROLLBACK;
