-- Read RPCs for the metrics worker, and lock the metrics_worker functions to the roles that need them.
--
-- The worker is moving from the project-wide service-role key to its AppOps API token, which runs
-- requests as the app's own role (app_metrics_worker_<env>). That role does not bypass RLS, and the
-- tables' "public can read" policies cover only anon and authenticated, so direct table reads as
-- the app role return an empty result. These SECURITY DEFINER functions give the worker its three
-- reads without adding role-specific policies. AppOps grants the app role EXECUTE on every function
-- in the schema (permissions.functions in apps/metrics-worker/appops.yaml).
--
-- Reverse: drop the three get_* functions. The REVOKEs at the end need no reversal; the worker never
-- calls these functions as anon or authenticated.

CREATE OR REPLACE FUNCTION metrics_worker.get_site_metrics()
  RETURNS SETOF metrics_worker.site_metrics
  LANGUAGE sql
  STABLE
  SECURITY DEFINER
  SET search_path TO 'metrics_worker'
AS $function$
  SELECT * FROM metrics_worker.site_metrics;
$function$;

CREATE OR REPLACE FUNCTION metrics_worker.get_site_event_counts(p_limit int DEFAULT 30)
  RETURNS TABLE (event text, count bigint)
  LANGUAGE sql
  STABLE
  SECURITY DEFINER
  SET search_path TO 'metrics_worker'
AS $function$
  SELECT c.event, c.count
  FROM metrics_worker.site_event_counts AS c
  ORDER BY c.count DESC
  LIMIT least(greatest(coalesce(p_limit, 30), 0), 100);
$function$;

CREATE OR REPLACE FUNCTION metrics_worker.get_worker_health_stats()
  RETURNS SETOF metrics_worker.worker_health_stats
  LANGUAGE sql
  STABLE
  SECURITY DEFINER
  SET search_path TO 'metrics_worker'
AS $function$
  SELECT * FROM metrics_worker.worker_health_stats;
$function$;

-- Supabase's default privileges grant EXECUTE on new functions to anon and authenticated explicitly,
-- so revoking from PUBLIC alone is not enough. On a local stack this let a request carrying only the
-- publishable key call the write RPCs. Only the worker (service_role today, its AppOps role next)
-- should call any of these.
REVOKE ALL ON FUNCTION metrics_worker.get_site_metrics() FROM PUBLIC, anon, authenticated;
REVOKE ALL ON FUNCTION metrics_worker.get_site_event_counts(int) FROM PUBLIC, anon, authenticated;
REVOKE ALL ON FUNCTION metrics_worker.get_worker_health_stats() FROM PUBLIC, anon, authenticated;
REVOKE ALL ON FUNCTION metrics_worker.record_site_events(jsonb) FROM PUBLIC, anon, authenticated;
REVOKE ALL ON FUNCTION metrics_worker.record_worker_health(text, int, int, int, boolean, boolean, boolean)
  FROM PUBLIC, anon, authenticated;

GRANT EXECUTE ON FUNCTION metrics_worker.get_site_metrics() TO service_role;
GRANT EXECUTE ON FUNCTION metrics_worker.get_site_event_counts(int) TO service_role;
GRANT EXECUTE ON FUNCTION metrics_worker.get_worker_health_stats() TO service_role;
GRANT EXECUTE ON FUNCTION metrics_worker.record_site_events(jsonb) TO service_role;
GRANT EXECUTE ON FUNCTION metrics_worker.record_worker_health(text, int, int, int, boolean, boolean, boolean)
  TO service_role;
