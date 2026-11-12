"""
Python equivalent of the PostHog proxy + Supabase metrics Cloudflare Worker.

Requires (wrangler.jsonc):
  "compatibility_flags": ["python_workers"]
  "main": "src/entry.py"

And in pyproject.toml:
  dependencies = ["workers-runtime-sdk"]
"""

import base64
import gzip
import json
import time
from datetime import UTC, datetime
from urllib.parse import parse_qs, unquote, urlparse

# PostgREST schema holding the metrics tables and RPCs. It must be an exposed API schema, and the
# profile headers below tell PostgREST to use it instead of the default `public` schema.
DB_SCHEMA = "metrics_worker"

try:
    from pyodide.ffi import create_proxy
    from workers import Response, WorkerEntrypoint, fetch
except ImportError:  # pragma: no cover
    # Stubs for local execution, linting, and testing outside Cloudflare Pyodide
    def create_proxy(obj):
        return obj

    class WorkerEntrypoint:
        pass

    class Response:
        def __init__(self, body=None, status=200, headers=None):
            self.body = body
            self.status = status
            self.headers = headers or {}

        @classmethod
        def json(cls, data, status=200, headers=None):
            return cls(json.dumps(data), status=status, headers=headers)

    async def fetch(*args, **kwargs):
        pass


ALLOWED_ORIGINS = {
    "https://jbirdkerr.net",
    "https://www.jbirdkerr.net",
    "https://boopmaster3000.jbirdkerr.net",
}

POSTHOG_API_HOST = "https://us.i.posthog.com"
POSTHOG_ASSETS_HOST = "https://us-assets.i.posthog.com"


class Default(WorkerEntrypoint):
    async def fetch(self, request):
        start_time = time.monotonic()
        url = urlparse(request.url)
        origin = request.headers.get("Origin")
        cors_headers = get_cors_headers(origin)

        if request.method == "OPTIONS":
            # Preflight: echo back whatever headers the browser says it's about
            # to send, rather than a hardcoded list. A hardcoded
            # Access-Control-Allow-Headers that doesn't cover every header the
            # actual request sends (e.g. PostHog SDK headers) causes the
            # browser to fail the preflight with "CORS Missing Allowed Header".
            requested_headers = request.headers.get("Access-Control-Request-Headers")
            preflight_headers = dict(cors_headers)
            if requested_headers:
                preflight_headers["Access-Control-Allow-Headers"] = requested_headers
            return Response(None, status=204, headers=preflight_headers)

        if url.path == "/health":
            response = Response.json(
                {
                    "status": "ok",
                    "timestamp": datetime.now(UTC).isoformat(),
                },
                headers=cors_headers,
            )
            elapsed_ms = (time.monotonic() - start_time) * 1000
            self.ctx.waitUntil(
                create_proxy(record_worker_health(self.env, "health", 200, latency_ms=elapsed_ms))
            )
            return response

        if url.path == "/metrics" and request.method == "GET":
            response = await handle_metrics(self.env, cors_headers)
            elapsed_ms = (time.monotonic() - start_time) * 1000
            self.ctx.waitUntil(
                create_proxy(
                    record_worker_health(
                        self.env, "metrics", response.status, latency_ms=elapsed_ms
                    )
                )
            )
            return response

        if url.path == "/health-metrics" and request.method == "GET":
            response = await handle_health_metrics(self.env, cors_headers)
            elapsed_ms = (time.monotonic() - start_time) * 1000
            self.ctx.waitUntil(
                create_proxy(
                    record_worker_health(
                        self.env, "metrics", response.status, latency_ms=elapsed_ms
                    )
                )
            )
            return response

        # PostHog's browser SDK sends capture traffic through api_host.
        # We forward it to PostHog and independently persist the same events
        # to Supabase so the metrics path does not depend on PostHog query latency.
        if is_posthog_path(url.path):
            response = await proxy_posthog(request, url, self.env, cors_headers, self.ctx)
            elapsed_ms = (time.monotonic() - start_time) * 1000
            posthog_failed = response.status >= 500
            self.ctx.waitUntil(
                create_proxy(
                    record_worker_health(
                        self.env,
                        "proxy",
                        response.status,
                        latency_ms=elapsed_ms,
                        posthog_proxy_failed=posthog_failed,
                    )
                )
            )
            return response

        self.ctx.waitUntil(create_proxy(record_worker_health(self.env, "not_found", 404)))
        return Response("Not Found", status=404, headers=cors_headers)


def get_cors_headers(origin):
    headers = {
        "Access-Control-Allow-Methods": "GET,POST,OPTIONS",
        "Access-Control-Allow-Headers": "Content-Type,Authorization,X-Requested-With",
        "Vary": "Origin",
    }

    if origin and origin in ALLOWED_ORIGINS:
        headers["Access-Control-Allow-Origin"] = origin

    return headers


def is_posthog_path(pathname):
    return (
        pathname.startswith("/static/")
        or pathname.startswith("/array/")
        or pathname.startswith("/e/")
        or pathname.startswith("/s/")
        or pathname.startswith("/flags/")
        or pathname == "/decide"
        or pathname == "/capture"
        or pathname == "/capture/"
        or pathname == "/batch"
        or pathname == "/batch/"
        or pathname == "/engage"
        or pathname.startswith("/i/")
    )


async def proxy_posthog(request, url, env, cors_headers, ctx):
    is_asset = url.path.startswith("/static/") or url.path.startswith("/array/")

    target_host = POSTHOG_ASSETS_HOST if is_asset else POSTHOG_API_HOST
    query = f"?{url.query}" if url.query else ""
    target_url = f"{target_host}{url.path}{query}"

    # request.headers is a JS Headers object exposed via FFI; it is iterable
    # as (name, value) pairs, and dict() will consume that directly.
    headers = dict(request.headers)
    headers.pop("host", None)
    headers.pop("origin", None)
    # request.arrayBuffer() transparently decompresses a gzip-encoded body
    # (common for PostHog's /s/ session-recording and /i/v0/e/ capture
    # traffic), so by the time we forward it the bytes are plain, not gzip.
    # Forwarding the original Content-Encoding header would tell PostHog to
    # gunzip already-plain data, producing an empty/garbage result and a
    # JSON parse failure on their end. Content-Length is dropped too since
    # the runtime recalculates it from the actual outgoing body anyway.
    headers.pop("content-encoding", None)
    headers.pop("content-length", None)

    body = None
    if request.method not in ("GET", "HEAD"):
        # .bytes() (not .arrayBuffer() — that's the raw JS method name and
        # doesn't exist on this Pythonic Request wrapper) reads the body as
        # raw bytes, binary-safe for compressed/non-UTF-8 payloads, and
        # returns native Python bytes directly.
        body = await request.bytes()

    # Make a best-effort copy of the event into Supabase. PostHog still gets
    # the original request even if the Supabase write fails.
    #
    # Path note: current posthog-js sends event captures to /i/v0/e/ by
    # default; /capture and /batch are older/alternate endpoints kept here
    # for compatibility. /s/ (session recordings) is deliberately excluded —
    # those aren't discrete "events" in the same shape. Confirm in your
    # Network tab which path your actual event captures use (a 200, not the
    # session-recording /s/ traffic) and add it here if it differs.
    persistable = (
        url.path.startswith("/i/v0/e")
        or url.path.startswith("/i/v0/batch")
        or url.path.startswith("/batch")
        or url.path.startswith("/capture")
        or url.path.startswith("/e")
        or url.path.startswith("/engage")
    )
    print(
        f"posthog proxy: method={request.method} path={url.path!r} "
        f"is_asset={is_asset} persistable={persistable}"
    )
    if request.method == "POST" and not is_asset and persistable:
        try:
            payload_bytes = body if body is not None else b""
            ctx.waitUntil(create_proxy(persist_posthog_payload(payload_bytes, env, url.query)))
        except Exception as error:
            print(f"Unable to queue Supabase persistence: {error}")

    response = await fetch(
        target_url,
        method=request.method,
        headers=headers,
        body=body,
        redirect="follow",
    )

    response_headers = {}
    for k, v in dict(response.headers).items():
        k_lower = k.lower()
        if not k_lower.startswith("access-control-") and k_lower not in (
            "content-encoding",
            "content-length",
            "transfer-encoding",
        ):
            response_headers[k] = v

    if is_asset:
        response_headers["Access-Control-Allow-Origin"] = "*"
        response_headers["Access-Control-Allow-Methods"] = "GET, OPTIONS"
        response_headers["Cache-Control"] = "public, max-age=86400"
    else:
        response_headers.update(cors_headers)

    return Response(response.body, status=response.status, headers=response_headers)


def decode_posthog_body(payload_bytes, query_string=None):
    """Decode a PostHog capture payload into a Python dict/list.

    Handles raw gzip bytes, direct JSON, form-encoded / base64 payloads,
    and query string data without corrupting base64 characters.
    """
    if isinstance(payload_bytes, memoryview):
        payload_bytes = bytes(payload_bytes)

    # 1. Try direct gzip decompress
    if payload_bytes:
        try:
            return json.loads(gzip.decompress(payload_bytes))
        except Exception:
            pass

    # 2. Try direct JSON parse
    if payload_bytes:
        try:
            return json.loads(payload_bytes)
        except Exception:
            pass

    # 3. Try string representations from body or query string
    candidates = []
    if payload_bytes:
        try:
            candidates.append(payload_bytes.decode("utf-8").strip())
        except UnicodeDecodeError:
            try:
                candidates.append(payload_bytes.decode("latin-1").strip())
            except Exception:
                pass

    if query_string:
        candidates.append(query_string.strip())

    for text in candidates:
        tokens = [text]
        if "data=" in text:
            # Extract the raw data parameter value without converting '+' into spaces
            extracted = text.split("data=", 1)[1].split("&", 1)[0]
            tokens.append(extracted)
            tokens.append(parse_qs(text).get("data", [""])[0])

        for token in tokens:
            if not token:
                continue

            # Try token directly as JSON
            try:
                return json.loads(token)
            except Exception:
                pass

            # Try URL-unquoted token as JSON
            try:
                unquoted = unquote(token)
                return json.loads(unquoted)
            except Exception:
                pass

            # Try Base64 (both verbatim and with spaces turned back to '+')
            for b64_cand in [token, token.replace(" ", "+")]:
                try:
                    padded = b64_cand + "=" * (-len(b64_cand) % 4)
                    decoded = base64.b64decode(padded)
                    try:
                        return json.loads(gzip.decompress(decoded))
                    except Exception:
                        pass
                    try:
                        return json.loads(decoded)
                    except Exception:
                        pass
                except Exception:
                    pass

    return None


async def persist_posthog_payload(payload_bytes, env, query_string=None):
    if not getattr(env, "SUPABASE_URL", None) or not getattr(
        env, "SUPABASE_SERVICE_ROLE_KEY", None
    ):
        print("Supabase environment variables are not configured")
        return

    payload = decode_posthog_body(payload_bytes, query_string)
    if payload is None:
        print("PostHog payload was not valid JSON or decodable data= form")
        await record_worker_health(env, "proxy", 200, decode_success=False)
        return

    events = normalize_posthog_payload(payload)
    if not events:
        await record_worker_health(env, "proxy", 200, decode_success=True)
        return

    persist_start = time.monotonic()
    supabase_failed = False
    try:
        response = await fetch(
            f"{env.SUPABASE_URL}/rest/v1/rpc/record_site_events",
            method="POST",
            headers=supabase_headers(env, write=True),
            body=json.dumps({"p_events": events}),
        )

        if not response.ok:
            error_text = await response.text()
            print(f"Supabase event persistence failed: {response.status} {error_text}")
            supabase_failed = True
    except Exception as error:
        print(f"Supabase event persistence exception: {error}")
        supabase_failed = True

    persist_ms = (time.monotonic() - persist_start) * 1000
    await record_worker_health(
        env,
        "proxy",
        200,
        decode_success=True,
        persist_latency_ms=persist_ms,
        supabase_write_failed=supabase_failed,
    )


def normalize_posthog_payload(payload):
    if isinstance(payload, list):
        raw_events = payload
    elif isinstance(payload, dict):
        if isinstance(payload.get("batch"), list):
            raw_events = payload["batch"]
        elif isinstance(payload.get("data"), list):
            raw_events = payload["data"]
        elif payload.get("event"):
            raw_events = [payload]
        else:
            raw_events = []
    else:
        raw_events = []

    events = []
    for item in raw_events:
        if not isinstance(item, dict):
            continue
        event = item.get("event")
        if not isinstance(event, str):
            continue
        properties = item.get("properties")
        events.append(
            {
                "event": event,
                "properties": properties if isinstance(properties, dict) else {},
                "occurred_at": item.get("timestamp"),
                "distinct_id": (
                    item.get("distinct_id") if isinstance(item.get("distinct_id"), str) else None
                ),
            }
        )
    return events


async def handle_metrics(env, cors_headers):
    if not getattr(env, "SUPABASE_URL", None) or not getattr(
        env, "SUPABASE_SERVICE_ROLE_KEY", None
    ):
        return Response.json(
            {"error": "Supabase configuration missing"},
            status=500,
            headers=cors_headers,
        )

    try:
        summary_response = await supabase_request(env, "/rest/v1/site_metrics?select=*")
        events_response = await supabase_request(
            env,
            "/rest/v1/site_event_counts?select=event,count&order=count.desc&limit=30",
        )

        if not summary_response.ok or not events_response.ok:
            raise Exception(
                f"Supabase metrics query failed: {summary_response.status}/{events_response.status}"
            )

        summary_rows = await summary_response.json()
        event_rows = await events_response.json()
        summary = summary_rows[0] if summary_rows else {}

        updated_at = summary.get("updated_at") or datetime.now(UTC).isoformat()

        return Response(
            render_metrics_html(summary, event_rows),
            headers={
                **cors_headers,
                "Content-Type": "text/html; charset=utf-8",
                "Cache-Control": "public, max-age=2, s-maxage=3, stale-while-revalidate=5",
                "X-Metrics-Generated": updated_at,
            },
        )
    except Exception as error:
        print(f"Metrics query failed: {error}")
        return Response(
            '<div class="error">⚠️ Unable to load metrics</div>',
            status=503,
            headers={
                **cors_headers,
                "Content-Type": "text/html; charset=utf-8",
                "Cache-Control": "no-store",
            },
        )


async def handle_health_metrics(env, cors_headers):
    if not getattr(env, "SUPABASE_URL", None) or not getattr(
        env, "SUPABASE_SERVICE_ROLE_KEY", None
    ):
        return Response.json(
            {"error": "Supabase configuration missing"},
            status=500,
            headers=cors_headers,
        )

    try:
        response = await supabase_request(env, "/rest/v1/worker_health_stats?select=*")
        if not response.ok:
            raise Exception(f"Supabase health query failed: {response.status}")

        rows = await response.json()
        stats = rows[0] if rows else {}

        return Response(
            render_health_metrics_html(stats),
            headers={
                **cors_headers,
                "Content-Type": "text/html; charset=utf-8",
                "Cache-Control": "public, max-age=2, s-maxage=3, stale-while-revalidate=5",
            },
        )
    except Exception as error:
        print(f"Health metrics query failed: {error}")
        return Response(
            '<div class="error">⚠️ Unable to load health metrics</div>',
            status=503,
            headers={
                **cors_headers,
                "Content-Type": "text/html; charset=utf-8",
                "Cache-Control": "no-store",
            },
        )


def supabase_headers(env, write=False):
    """Headers for PostgREST calls to the metrics schema.

    PostgREST selects a non-default schema with Accept-Profile (GET/HEAD) or
    Content-Profile (POST, PATCH, DELETE, and RPC calls).
    """
    headers = {
        "apikey": env.SUPABASE_SERVICE_ROLE_KEY,
        "Authorization": f"Bearer {env.SUPABASE_SERVICE_ROLE_KEY}",
    }
    if write:
        headers["Content-Type"] = "application/json"
        headers["Content-Profile"] = DB_SCHEMA
    else:
        headers["Accept-Profile"] = DB_SCHEMA
    return headers


async def supabase_request(env, path, method="GET"):
    return await fetch(
        f"{env.SUPABASE_URL}{path}",
        method=method,
        headers=supabase_headers(env),
    )


async def record_worker_health(
    env,
    request_type,
    status_code,
    latency_ms=None,
    persist_latency_ms=None,
    decode_success=None,
    supabase_write_failed=False,
    posthog_proxy_failed=False,
):
    """Fire-and-forget health stats recording to Supabase."""
    if not getattr(env, "SUPABASE_URL", None) or not getattr(
        env, "SUPABASE_SERVICE_ROLE_KEY", None
    ):
        return
    try:
        body = {
            "p_request_type": request_type,
            "p_status_code": status_code,
        }
        if latency_ms is not None:
            body["p_latency_ms"] = int(latency_ms)
        if persist_latency_ms is not None:
            body["p_persist_latency_ms"] = int(persist_latency_ms)
        if decode_success is not None:
            body["p_decode_success"] = decode_success
        if supabase_write_failed:
            body["p_supabase_write_failed"] = True
        if posthog_proxy_failed:
            body["p_posthog_proxy_failed"] = True

        await fetch(
            f"{env.SUPABASE_URL}/rest/v1/rpc/record_worker_health",
            method="POST",
            headers=supabase_headers(env, write=True),
            body=json.dumps(body),
        )
    except Exception as error:
        print(f"Worker health recording failed: {error}")


def render_health_metrics_html(stats):
    total_requests = number(stats.get("total_requests"))
    total_proxy = number(stats.get("total_proxy_requests"))
    total_metrics = number(stats.get("total_metrics_requests"))
    total_health = number(stats.get("total_health_requests"))
    errors_4xx = number(stats.get("total_errors_4xx"))
    errors_5xx = number(stats.get("total_errors_5xx"))
    sb_failures = number(stats.get("total_supabase_write_failures"))
    ph_failures = number(stats.get("total_posthog_proxy_failures"))
    decode_fail = number(stats.get("total_decode_failures"))
    decode_ok = number(stats.get("total_decode_successes"))

    # Compute averages
    sum_metrics_lat = number(stats.get("sum_metrics_latency_ms"))
    cnt_metrics_lat = number(stats.get("count_metrics_latency"))
    avg_metrics_ms = (sum_metrics_lat / cnt_metrics_lat) if cnt_metrics_lat > 0 else 0

    sum_proxy_lat = number(stats.get("sum_proxy_latency_ms"))
    cnt_proxy_lat = number(stats.get("count_proxy_latency"))
    avg_proxy_ms = (sum_proxy_lat / cnt_proxy_lat) if cnt_proxy_lat > 0 else 0

    sum_persist_lat = number(stats.get("sum_persist_latency_ms"))
    cnt_persist_lat = number(stats.get("count_persist_latency"))
    avg_persist_ms = (sum_persist_lat / cnt_persist_lat) if cnt_persist_lat > 0 else 0

    # Error rate
    error_rate = ((errors_4xx + errors_5xx) / total_requests * 100) if total_requests > 0 else 0

    # Decode success rate
    total_decode = decode_ok + decode_fail
    decode_rate = (decode_ok / total_decode * 100) if total_decode > 0 else 100

    html = f"""
    <h3>📡 Request Volume</h3>
    <div class="metric-row"><span class="metric-label">Total Requests</span><span class="metric-value">{fmt(total_requests)}</span></div>
    <div class="feature-grid">
      <div class="feature-box"><div class="feature-name">PostHog Proxy</div><div class="feature-count">{fmt(total_proxy)}</div></div>
      <div class="feature-box"><div class="feature-name">Dashboard</div><div class="feature-count">{fmt(total_metrics)}</div></div>
      <div class="feature-box"><div class="feature-name">Health Checks</div><div class="feature-count">{fmt(total_health)}</div></div>
      <div class="feature-box"><div class="feature-name">Error Rate</div><div class="feature-count">{error_rate:.1f}%</div></div>
    </div>
    <h3>⏱️ Avg Latency</h3>
    <div class="metric-row"><span class="metric-label">Dashboard Render</span><span class="metric-value">{avg_metrics_ms:.0f} ms</span></div>
    <div class="metric-row"><span class="metric-label">PostHog Proxy Round-Trip</span><span class="metric-value">{avg_proxy_ms:.0f} ms</span></div>
    <div class="metric-row"><span class="metric-label">Supabase Persist (bg)</span><span class="metric-value">{avg_persist_ms:.0f} ms</span></div>
    <h3>🛡️ Pipeline Health</h3>
    <div class="metric-row"><span class="metric-label">Payload Decode Success Rate</span><span class="metric-value">{decode_rate:.1f}%</span></div>
    <div class="metric-row"><span class="metric-label">4xx Errors</span><span class="metric-value">{fmt(errors_4xx)}</span></div>
    <div class="metric-row"><span class="metric-label">5xx Errors</span><span class="metric-value">{fmt(errors_5xx)}</span></div>
    <div class="metric-row"><span class="metric-label">Supabase Write Failures</span><span class="metric-value">{fmt(sb_failures)}</span></div>
    <div class="metric-row"><span class="metric-label">PostHog Proxy Failures</span><span class="metric-value">{fmt(ph_failures)}</span></div>
    """

    raw_updated_at = stats.get("updated_at") or datetime.now(UTC).isoformat()
    updated_at = stats.get("updated_at")
    if updated_at:
        try:
            updated = datetime.fromisoformat(updated_at.replace("Z", "+00:00")).strftime("%H:%M:%S")
        except ValueError:
            updated = updated_at
    else:
        updated = "—"

    html += f'<div style="margin-top:16px;padding-top:12px;border-top:1px solid rgba(0,212,255,.2);font-size:.8rem;color:#666;">Last updated: <span class="metrics-timestamp" data-utc="{escape_html(raw_updated_at)}">{escape_html(updated)}</span></div>'
    return html


def render_metrics_html(summary, event_rows):
    total_boops = number(summary.get("total_boops"))
    max_boop_combo = number(summary.get("max_boop_combo"))
    total_events = number(summary.get("total_events"))
    eye_tracking_toggles = number(summary.get("eye_tracking_toggles"))
    googly_eye_toggles = number(
        summary.get("googly_eyes_toggles", summary.get("googly_eye_toggles"))
    )
    party_mode_toggles = number(summary.get("party_mode_toggles"))
    treat_showers = number(summary.get("treat_showers"))
    total_eye_distance = number(summary.get("total_eye_distance"))

    distance_m = total_eye_distance / 3780

    raw_updated_at = summary.get("updated_at") or datetime.now(UTC).isoformat()
    updated_at = summary.get("updated_at")
    if updated_at:
        try:
            updated = datetime.fromisoformat(updated_at.replace("Z", "+00:00")).strftime("%H:%M:%S")
        except ValueError:
            updated = updated_at
    else:
        updated = "—"

    html = f"""
    <div class="metric-row"><span class="metric-label">Total Boops</span><span class="metric-value">{fmt(total_boops)}</span></div>
    <div class="metric-row"><span class="metric-label">Total Events Tracked</span><span class="metric-value">{fmt(total_events)}</span></div>
    <div class="metric-row"><span class="metric-label">Max Boop Combo</span><span class="metric-value">×{fmt(max_boop_combo)}</span></div>
    <div class="metric-row"><span class="metric-label">👀 Eyeball Distance Rolled</span><span class="metric-value">{distance_m:.2f} m <small>({total_eye_distance:,.0f} px)</small></span></div>
    <h3>🎮 Feature Toggles</h3><div class="feature-grid">
      <div class="feature-box"><div class="feature-name">Eye Tracking</div><div class="feature-count">{fmt(eye_tracking_toggles)}</div></div>
      <div class="feature-box"><div class="feature-name">Googly Eyes</div><div class="feature-count">{fmt(googly_eye_toggles)}</div></div>
      <div class="feature-box"><div class="feature-name">Party Mode</div><div class="feature-count">{fmt(party_mode_toggles)}</div></div>
      <div class="feature-box"><div class="feature-name">Treats</div><div class="feature-count">{fmt(treat_showers)}</div></div>
    </div><h3>📈 Event Breakdown</h3>
    """
    custom_events = [
        r
        for r in event_rows
        if (r.get("event") or "").strip() and not (r.get("event") or "").strip().startswith("$")
    ][:10]
    for row in custom_events:
        event = escape_html(row.get("event") or "")
        count = fmt(number(row.get("count")))
        html += f'<div class="metric-row"><span class="metric-label">{event}</span><span class="metric-value">{count}</span></div>'
    html += f'<div style="margin-top:16px;padding-top:12px;border-top:1px solid rgba(0,212,255,.2);font-size:.8rem;color:#666;">Last updated: <span class="metrics-timestamp" data-utc="{escape_html(raw_updated_at)}">{escape_html(updated)}</span></div>'
    return html


def number(value):
    try:
        return float(value) if value is not None else 0
    except (TypeError, ValueError):
        return 0


def fmt(value):
    return f"{value:,.0f}" if value == int(value) else f"{value:,}"


def escape_html(value):
    return (
        str(value)
        .replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
        .replace("'", "&#039;")
    )
