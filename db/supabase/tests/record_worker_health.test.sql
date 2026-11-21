-- metrics_worker.record_worker_health: each call bumps the right counters on the
-- singleton worker_health_stats row. Runs in a transaction that is rolled back.
BEGIN;
CREATE EXTENSION IF NOT EXISTS pgtap WITH SCHEMA extensions;
SET search_path TO extensions, public;

SELECT plan(22);

-- Start from a known row so the assertions don't depend on seed data.
INSERT INTO metrics_worker.worker_health_stats (id) VALUES (true) ON CONFLICT (id) DO NOTHING;
UPDATE metrics_worker.worker_health_stats SET
  total_requests = 0, total_proxy_requests = 0, total_metrics_requests = 0,
  total_health_requests = 0, total_errors_4xx = 0, total_errors_5xx = 0,
  total_supabase_write_failures = 0, total_posthog_proxy_failures = 0,
  total_decode_failures = 0, total_decode_successes = 0,
  sum_metrics_latency_ms = 0, count_metrics_latency = 0,
  sum_proxy_latency_ms = 0, count_proxy_latency = 0,
  sum_persist_latency_ms = 0, count_persist_latency = 0,
  updated_at = '2000-01-01'
WHERE id = true;

-- The same calls the worker makes (apps/metrics-worker/worker.py).
SELECT metrics_worker.record_worker_health('health', 200, p_latency_ms => 3);
SELECT metrics_worker.record_worker_health('metrics', 200, p_latency_ms => 40);
SELECT metrics_worker.record_worker_health('metrics', 503, p_latency_ms => 60);
SELECT metrics_worker.record_worker_health('proxy', 200, p_latency_ms => 25);
SELECT metrics_worker.record_worker_health('proxy', 502, p_latency_ms => 75, p_posthog_proxy_failed => true);
SELECT metrics_worker.record_worker_health('proxy', 200, p_decode_success => true, p_persist_latency_ms => 12);
SELECT metrics_worker.record_worker_health('proxy', 200, p_decode_success => true, p_persist_latency_ms => 8, p_supabase_write_failed => true);
SELECT metrics_worker.record_worker_health('proxy', 200, p_decode_success => false);
SELECT metrics_worker.record_worker_health('not_found', 404);

SELECT is(total_requests, 9::bigint, 'every call counts as a request') FROM metrics_worker.worker_health_stats;
SELECT is(total_health_requests, 1::bigint, 'health requests') FROM metrics_worker.worker_health_stats;
SELECT is(total_metrics_requests, 2::bigint, 'metrics requests') FROM metrics_worker.worker_health_stats;
SELECT is(total_proxy_requests, 5::bigint, 'proxy requests') FROM metrics_worker.worker_health_stats;
SELECT is(total_errors_4xx, 1::bigint, '4xx counts only 400-499') FROM metrics_worker.worker_health_stats;
SELECT is(total_errors_5xx, 2::bigint, '5xx counts 500 and up') FROM metrics_worker.worker_health_stats;
SELECT is(total_posthog_proxy_failures, 1::bigint, 'PostHog proxy failures') FROM metrics_worker.worker_health_stats;
SELECT is(total_supabase_write_failures, 1::bigint, 'Supabase write failures') FROM metrics_worker.worker_health_stats;
SELECT is(total_decode_successes, 2::bigint, 'decode successes') FROM metrics_worker.worker_health_stats;
SELECT is(total_decode_failures, 1::bigint, 'decode failures; NULL decode_success counts as neither') FROM metrics_worker.worker_health_stats;

SELECT is(sum_metrics_latency_ms, 100::bigint, 'metrics latency sum') FROM metrics_worker.worker_health_stats;
SELECT is(count_metrics_latency, 2::bigint, 'metrics latency samples') FROM metrics_worker.worker_health_stats;
SELECT is(sum_proxy_latency_ms, 100::bigint, 'proxy latency sum ignores calls without latency') FROM metrics_worker.worker_health_stats;
SELECT is(count_proxy_latency, 2::bigint, 'proxy latency samples') FROM metrics_worker.worker_health_stats;
SELECT is(sum_persist_latency_ms, 20::bigint, 'persist latency sum') FROM metrics_worker.worker_health_stats;
SELECT is(count_persist_latency, 2::bigint, 'persist latency samples') FROM metrics_worker.worker_health_stats;
SELECT ok(updated_at > '2000-01-01', 'updated_at moves forward') FROM metrics_worker.worker_health_stats;

-- Health latency is accepted but not aggregated anywhere.
SELECT is(
  (SELECT sum_metrics_latency_ms + sum_proxy_latency_ms FROM metrics_worker.worker_health_stats),
  200::bigint,
  'health-check latency is not folded into metrics or proxy latency'
);

-- Defaults: a bare call only bumps the request counters.
UPDATE metrics_worker.worker_health_stats SET total_decode_failures = 0, total_decode_successes = 0,
  total_supabase_write_failures = 0, total_posthog_proxy_failures = 0 WHERE id = true;
SELECT metrics_worker.record_worker_health('proxy', 200);
SELECT is(
  (SELECT total_decode_failures + total_decode_successes FROM metrics_worker.worker_health_stats),
  0::bigint,
  'omitted decode_success does not count as success or failure'
);
SELECT is(
  (SELECT total_supabase_write_failures + total_posthog_proxy_failures FROM metrics_worker.worker_health_stats),
  0::bigint,
  'failure flags default to false'
);

-- Unknown request types still count as requests but not under any type.
SELECT metrics_worker.record_worker_health('mystery', 200);
SELECT is(total_requests, 11::bigint, 'unknown request type still counts as a request') FROM metrics_worker.worker_health_stats;
SELECT is(
  (SELECT total_health_requests + total_metrics_requests + total_proxy_requests FROM metrics_worker.worker_health_stats),
  9::bigint,
  'unknown request type is not attributed to proxy, metrics, or health'
);

SELECT * FROM finish();
ROLLBACK;
