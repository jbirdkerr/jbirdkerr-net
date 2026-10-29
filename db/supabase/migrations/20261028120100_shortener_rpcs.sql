-- RPCs for the URL shortener. All are SECURITY DEFINER with a pinned search_path,
-- and executable only by the service role.

-- Record one click: upsert into the daily aggregate, incrementing by 1.
CREATE OR REPLACE FUNCTION shortener.record_click(
  p_slug          text,
  p_day           date,
  p_country       text DEFAULT 'unknown',
  p_referrer_host text DEFAULT 'direct'
)
  RETURNS void
  LANGUAGE plpgsql
  SECURITY DEFINER
  SET search_path TO 'shortener'
AS $function$
BEGIN
  INSERT INTO shortener.click_daily (slug, day, country, referrer_host, clicks)
  VALUES (
    p_slug,
    p_day,
    COALESCE(NULLIF(p_country, ''), 'unknown'),
    COALESCE(NULLIF(p_referrer_host, ''), 'direct'),
    1
  )
  ON CONFLICT (slug, day, country, referrer_host)
  DO UPDATE SET clicks = shortener.click_daily.clicks + 1;
END;
$function$;

-- Fixed-window per-key rate limit. Counts the attempt, then reports whether it
-- is within the limit and how many seconds remain in the current window.
CREATE OR REPLACE FUNCTION shortener.check_rate_limit(
  p_key_hash       text,
  p_limit          int,
  p_window_seconds int
)
  RETURNS TABLE (allowed boolean, retry_after int)
  LANGUAGE plpgsql
  SECURITY DEFINER
  SET search_path TO 'shortener'
AS $function$
DECLARE
  v_window_start timestamptz;
  v_hits         int;
BEGIN
  IF p_window_seconds < 1 THEN
    RAISE EXCEPTION 'p_window_seconds must be >= 1';
  END IF;

  v_window_start := to_timestamp(
    floor(extract(epoch FROM now()) / p_window_seconds) * p_window_seconds
  );

  INSERT INTO shortener.rate_limits AS r (key_hash, window_start, hits)
  VALUES (p_key_hash, v_window_start, 1)
  ON CONFLICT (key_hash, window_start)
  DO UPDATE SET hits = r.hits + 1
  RETURNING r.hits INTO v_hits;

  -- Drop this key's expired windows
  DELETE FROM shortener.rate_limits
  WHERE key_hash = p_key_hash AND window_start < v_window_start;

  RETURN QUERY SELECT
    v_hits <= p_limit,
    GREATEST(1, ceil(extract(epoch FROM (
      v_window_start + make_interval(secs => p_window_seconds) - now()
    )))::int);
END;
$function$;

-- Aggregates for one link over [p_from, p_to] (inclusive UTC days) as one JSON object.
-- Days with no clicks are omitted here; the Worker zero-fills them.
CREATE OR REPLACE FUNCTION shortener.link_stats(
  p_slug  text,
  p_from  date,
  p_to    date,
  p_top_n int DEFAULT 10
)
  RETURNS jsonb
  LANGUAGE sql
  STABLE
  SECURITY DEFINER
  SET search_path TO 'shortener'
AS $function$
  SELECT jsonb_build_object(
    'total_clicks', COALESCE((
      SELECT sum(clicks) FROM shortener.click_daily WHERE slug = p_slug
    ), 0)::bigint,
    'clicks_in_range', COALESCE((
      SELECT sum(clicks) FROM shortener.click_daily
      WHERE slug = p_slug AND day BETWEEN p_from AND p_to
    ), 0)::bigint,
    'daily', COALESCE((
      SELECT jsonb_agg(jsonb_build_object('day', d.day, 'clicks', d.c) ORDER BY d.day)
      FROM (
        SELECT day, sum(clicks)::bigint AS c
        FROM shortener.click_daily
        WHERE slug = p_slug AND day BETWEEN p_from AND p_to
        GROUP BY day
      ) d
    ), '[]'::jsonb),
    'top_referrers', COALESCE((
      SELECT jsonb_agg(jsonb_build_object('host', t.referrer_host, 'clicks', t.c)
                       ORDER BY t.c DESC, t.referrer_host)
      FROM (
        SELECT referrer_host, sum(clicks)::bigint AS c
        FROM shortener.click_daily
        WHERE slug = p_slug AND day BETWEEN p_from AND p_to
        GROUP BY referrer_host
        ORDER BY c DESC, referrer_host
        LIMIT p_top_n
      ) t
    ), '[]'::jsonb),
    'top_countries', COALESCE((
      SELECT jsonb_agg(jsonb_build_object('country', t.country, 'clicks', t.c)
                       ORDER BY t.c DESC, t.country)
      FROM (
        SELECT country, sum(clicks)::bigint AS c
        FROM shortener.click_daily
        WHERE slug = p_slug AND day BETWEEN p_from AND p_to
        GROUP BY country
        ORDER BY c DESC, country
        LIMIT p_top_n
      ) t
    ), '[]'::jsonb)
  );
$function$;

REVOKE ALL ON FUNCTION shortener.record_click(text, date, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION shortener.record_click(text, date, text, text) TO "service_role", "postgres";

REVOKE ALL ON FUNCTION shortener.check_rate_limit(text, int, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION shortener.check_rate_limit(text, int, int) TO "service_role", "postgres";

REVOKE ALL ON FUNCTION shortener.link_stats(text, date, date, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION shortener.link_stats(text, date, date, int) TO "service_role", "postgres";
