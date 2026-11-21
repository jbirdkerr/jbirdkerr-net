-- Access rules for the metrics_worker schema: only the service role (the worker)
-- can call the write functions, the public roles can read the dashboard tables but
-- not write them, and the SECURITY DEFINER functions pin their search_path.
-- Runs in a transaction that is rolled back.
BEGIN;
CREATE EXTENSION IF NOT EXISTS pgtap WITH SCHEMA extensions;
SET search_path TO extensions, public;

SELECT plan(20);

-- Write functions
SELECT function_privs_are('metrics_worker', 'record_site_events', ARRAY['jsonb'], 'anon', ARRAY[]::text[],
  'anon cannot call record_site_events');
SELECT function_privs_are('metrics_worker', 'record_site_events', ARRAY['jsonb'], 'authenticated', ARRAY[]::text[],
  'authenticated cannot call record_site_events');
SELECT function_privs_are('metrics_worker', 'record_site_events', ARRAY['jsonb'], 'service_role', ARRAY['EXECUTE'],
  'service_role can call record_site_events');

SELECT function_privs_are('metrics_worker', 'record_worker_health',
  ARRAY['text', 'integer', 'integer', 'integer', 'boolean', 'boolean', 'boolean'], 'anon', ARRAY[]::text[],
  'anon cannot call record_worker_health');
SELECT function_privs_are('metrics_worker', 'record_worker_health',
  ARRAY['text', 'integer', 'integer', 'integer', 'boolean', 'boolean', 'boolean'], 'authenticated', ARRAY[]::text[],
  'authenticated cannot call record_worker_health');
SELECT function_privs_are('metrics_worker', 'record_worker_health',
  ARRAY['text', 'integer', 'integer', 'integer', 'boolean', 'boolean', 'boolean'], 'service_role', ARRAY['EXECUTE'],
  'service_role can call record_worker_health');

SELECT is_definer('metrics_worker', 'record_site_events', ARRAY['jsonb'],
  'record_site_events runs as its owner');
SELECT is_definer('metrics_worker', 'record_worker_health',
  ARRAY['text', 'integer', 'integer', 'integer', 'boolean', 'boolean', 'boolean'],
  'record_worker_health runs as its owner');
SELECT is(
  (SELECT proconfig FROM pg_proc WHERE oid = 'metrics_worker.record_site_events(jsonb)'::regprocedure),
  ARRAY['search_path=metrics_worker'],
  'record_site_events pins search_path to metrics_worker');
SELECT is(
  (SELECT proconfig FROM pg_proc
    WHERE oid = 'metrics_worker.record_worker_health(text, int, int, int, boolean, boolean, boolean)'::regprocedure),
  ARRAY['search_path=metrics_worker'],
  'record_worker_health pins search_path to metrics_worker');

-- Row level security is on for every table in the schema.
SELECT is(
  (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'metrics_worker' AND c.relkind = 'r' AND NOT c.relrowsecurity),
  0::bigint,
  'every metrics_worker table has row level security enabled');

-- Known rows to read back as anon.
INSERT INTO metrics_worker.site_metrics (id) VALUES (true) ON CONFLICT (id) DO NOTHING;
INSERT INTO metrics_worker.worker_health_stats (id) VALUES (true) ON CONFLICT (id) DO NOTHING;
INSERT INTO metrics_worker.site_event_counts (event, count) VALUES ('security_test', 1)
  ON CONFLICT (event) DO NOTHING;
INSERT INTO metrics_worker.site_events (event, properties) VALUES ('security_test', '{"secret": true}');

SET LOCAL ROLE anon;

SELECT is((SELECT count(*) FROM metrics_worker.site_metrics), 1::bigint,
  'anon can read site_metrics (public dashboard)');
SELECT is((SELECT count(*) FROM metrics_worker.worker_health_stats), 1::bigint,
  'anon can read worker_health_stats (public dashboard)');
SELECT ok((SELECT count(*) FROM metrics_worker.site_event_counts WHERE event = 'security_test') = 1,
  'anon can read site_event_counts (public dashboard)');
SELECT is((SELECT count(*) FROM metrics_worker.site_events), 0::bigint,
  'anon cannot read raw site_events');

SELECT throws_ok(
  $$INSERT INTO metrics_worker.site_events (event, properties) VALUES ('forged', '{}')$$,
  '42501', NULL, 'anon cannot insert site_events');
SELECT throws_ok(
  $$INSERT INTO metrics_worker.site_event_counts (event, count) VALUES ('forged', 1000000)$$,
  '42501', NULL, 'anon cannot insert site_event_counts');

-- RLS turns writes to rows anon can't target into silent no-ops; check nothing changed.
UPDATE metrics_worker.site_metrics SET total_boops = 999999999;
UPDATE metrics_worker.worker_health_stats SET total_requests = 999999999;
RESET ROLE;
SELECT isnt((SELECT total_boops FROM metrics_worker.site_metrics), 999999999::bigint,
  'anon cannot update site_metrics');
SELECT isnt((SELECT total_requests FROM metrics_worker.worker_health_stats), 999999999::bigint,
  'anon cannot update worker_health_stats');

SET LOCAL ROLE anon;
SELECT throws_ok(
  $$SELECT metrics_worker.record_site_events('[{"event": "forged"}]')$$,
  '42501', NULL, 'anon is refused when calling record_site_events directly');
RESET ROLE;

SELECT * FROM finish();
ROLLBACK;
