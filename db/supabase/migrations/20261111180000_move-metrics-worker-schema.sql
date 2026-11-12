-- Move the metrics-worker objects from the `public` schema into its own `metrics_worker` schema.
--
-- AppOps creates and owns the schema's roles and grants; this migration only moves objects. The
-- worker must send `Accept-Profile`/`Content-Profile: metrics_worker` (see apps/metrics-worker/worker.py),
-- and `metrics_worker` must be an exposed API schema (db/supabase/config.toml and the hosted project).
--
-- Reverse: infra/appops/docs/phase6/rollback.sql

CREATE SCHEMA IF NOT EXISTS metrics_worker;

-- PostgREST roles need USAGE to reach objects in the schema; table and function privileges
-- move with the objects.
GRANT USAGE ON SCHEMA metrics_worker TO anon, authenticated, service_role;

-- Tables keep their data, policies, RLS settings, ACLs, publication membership and owned sequences.
ALTER TABLE public.site_event_counts SET SCHEMA metrics_worker;
ALTER TABLE public.site_events SET SCHEMA metrics_worker;
ALTER TABLE public.site_metrics SET SCHEMA metrics_worker;
ALTER TABLE public.worker_health_stats SET SCHEMA metrics_worker;

-- Moving a function does not rewrite its body, which still names `public` objects and
-- `search_path`. Move it (keeping its ACL), then replace the body.
ALTER FUNCTION public.record_site_events(jsonb) SET SCHEMA metrics_worker;
ALTER FUNCTION public.record_worker_health(text, int, int, int, boolean, boolean, boolean) SET SCHEMA metrics_worker;

CREATE OR REPLACE FUNCTION metrics_worker.record_site_events (
  p_events jsonb
)
  RETURNS jsonb
  LANGUAGE plpgsql
  SECURITY DEFINER
  SET search_path TO 'metrics_worker'
  AS $function$
declare
    item jsonb;
    event_name text;
    event_properties jsonb;
    event_time timestamptz;
    event_distinct_id text;
    boop_combo bigint;
    eye_distance bigint;
    feature text;
    enabled boolean;
    current_metrics metrics_worker.site_metrics;
begin
    if jsonb_typeof(p_events) <> 'array' then
        raise exception 'p_events must be a JSON array';
    end if;

    for item in select value from jsonb_array_elements(p_events)
    loop
        event_name := nullif(item->>'event', '');
        if event_name is null then
            continue;
        end if;

        event_properties := coalesce(item->'properties', '{}'::jsonb);

        begin
            event_time := coalesce(
                (item->>'occurred_at')::timestamptz,
                now()
            );
        exception when others then
            event_time := now();
        end;

        event_distinct_id := nullif(item->>'distinct_id', '');

        insert into metrics_worker.site_events (
            occurred_at,
            event,
            distinct_id,
            properties
        )
        values (
            event_time,
            event_name,
            event_distinct_id,
            event_properties
        );

        insert into metrics_worker.site_event_counts (event, count)
        values (event_name, 1)
        on conflict (event)
        do update set count = site_event_counts.count + 1;

        update metrics_worker.site_metrics
        set
            total_events = total_events + 1,
            total_boops = total_boops +
                case when event_name = 'boop' then 1 else 0 end,
            max_boop_combo =
                case
                    when event_name = 'boop' then greatest(
                        max_boop_combo,
                        coalesce(nullif(event_properties->>'combo_count', '')::bigint, 0)
                    )
                    else max_boop_combo
                end,
            eye_tracking_toggles = eye_tracking_toggles +
                case
                    when event_name = 'feature_toggle'
                     and event_properties->>'feature' = 'eye_tracking'
                     and coalesce((event_properties->>'enabled')::boolean, false)
                    then 1 else 0
                end,
            googly_eyes_toggles = googly_eyes_toggles +
                case
                    when event_name = 'feature_toggle'
                     and event_properties->>'feature' = 'googly_eyes'
                     and coalesce((event_properties->>'enabled')::boolean, false)
                    then 1 else 0
                end,
            party_mode_toggles = party_mode_toggles +
                case
                    when event_name = 'feature_toggle'
                     and event_properties->>'feature' = 'party_mode'
                     and coalesce((event_properties->>'enabled')::boolean, false)
                    then 1 else 0
                end,
            treat_showers = treat_showers +
                case when event_name = 'treat_shower' then 1 else 0 end,
            total_eye_distance = total_eye_distance +
                case
                    when event_name = 'eye_movement' then
                        coalesce(nullif(event_properties->>'distance_px', '')::bigint, 0)
                    else 0
                end,
            updated_at = now()
        where id = true;
    end loop;

    select * into current_metrics
    from metrics_worker.site_metrics
    where id = true;

    return to_jsonb(current_metrics);
end;
$function$;

CREATE OR REPLACE FUNCTION metrics_worker.record_worker_health(
  p_request_type          text,     -- 'proxy', 'metrics', 'health', 'not_found'
  p_status_code           int,
  p_latency_ms            int DEFAULT NULL,
  p_persist_latency_ms    int DEFAULT NULL,
  p_decode_success        boolean DEFAULT NULL,
  p_supabase_write_failed boolean DEFAULT false,
  p_posthog_proxy_failed  boolean DEFAULT false
)
  RETURNS void
  LANGUAGE plpgsql
  SECURITY DEFINER
  SET search_path TO 'metrics_worker'
AS $function$
BEGIN
  UPDATE metrics_worker.worker_health_stats SET
    total_requests           = total_requests + 1,
    total_proxy_requests     = total_proxy_requests +
      CASE WHEN p_request_type = 'proxy' THEN 1 ELSE 0 END,
    total_metrics_requests   = total_metrics_requests +
      CASE WHEN p_request_type = 'metrics' THEN 1 ELSE 0 END,
    total_health_requests    = total_health_requests +
      CASE WHEN p_request_type = 'health' THEN 1 ELSE 0 END,
    total_errors_4xx         = total_errors_4xx +
      CASE WHEN p_status_code >= 400 AND p_status_code < 500 THEN 1 ELSE 0 END,
    total_errors_5xx         = total_errors_5xx +
      CASE WHEN p_status_code >= 500 THEN 1 ELSE 0 END,
    total_supabase_write_failures = total_supabase_write_failures +
      CASE WHEN p_supabase_write_failed THEN 1 ELSE 0 END,
    total_posthog_proxy_failures = total_posthog_proxy_failures +
      CASE WHEN p_posthog_proxy_failed THEN 1 ELSE 0 END,
    total_decode_failures    = total_decode_failures +
      CASE WHEN p_decode_success IS NOT NULL AND NOT p_decode_success THEN 1 ELSE 0 END,
    total_decode_successes   = total_decode_successes +
      CASE WHEN p_decode_success IS NOT NULL AND p_decode_success THEN 1 ELSE 0 END,
    sum_metrics_latency_ms   = sum_metrics_latency_ms +
      CASE WHEN p_request_type = 'metrics' AND p_latency_ms IS NOT NULL THEN p_latency_ms ELSE 0 END,
    count_metrics_latency    = count_metrics_latency +
      CASE WHEN p_request_type = 'metrics' AND p_latency_ms IS NOT NULL THEN 1 ELSE 0 END,
    sum_proxy_latency_ms     = sum_proxy_latency_ms +
      CASE WHEN p_request_type = 'proxy' AND p_latency_ms IS NOT NULL THEN p_latency_ms ELSE 0 END,
    count_proxy_latency      = count_proxy_latency +
      CASE WHEN p_request_type = 'proxy' AND p_latency_ms IS NOT NULL THEN 1 ELSE 0 END,
    sum_persist_latency_ms   = sum_persist_latency_ms +
      CASE WHEN p_persist_latency_ms IS NOT NULL THEN p_persist_latency_ms ELSE 0 END,
    count_persist_latency    = count_persist_latency +
      CASE WHEN p_persist_latency_ms IS NOT NULL THEN 1 ELSE 0 END,
    updated_at = now()
  WHERE id = true;
END;
$function$;
