"""Tests for the worker's request handling: routing, the PostHog proxy, Supabase
persistence, and the metrics endpoints.

`fetch` is replaced with a recorder that returns canned responses, so nothing here
touches the network. Background work queued with ctx.waitUntil() is awaited by the
tests, so its side effects (health recording, event persistence) are checked too.
"""

import asyncio
import json
import types
import unittest
from unittest import mock

import worker

SUPABASE_URL = "https://db.example.test"


class FakeFetchResponse:
    def __init__(self, status=200, body=b"", headers=None, json_body=None):
        self.status = status
        self.ok = 200 <= status < 300
        self.body = body
        self.headers = headers or {}
        self._json = json_body

    async def json(self):
        return self._json

    async def text(self):
        return self.body.decode() if isinstance(self.body, bytes) else str(self.body)


class FetchRecorder:
    """Stands in for the Workers runtime fetch().

    Routes are matched by URL substring in insertion order; unmatched URLs get a 200.
    """

    def __init__(self, routes=None):
        self.routes = routes or {}
        self.calls = []

    async def __call__(self, url, **kwargs):
        self.calls.append(types.SimpleNamespace(url=url, **kwargs))
        for fragment, response in self.routes.items():
            if fragment in url:
                if isinstance(response, Exception):
                    raise response
                return response
        return FakeFetchResponse()

    def to(self, fragment):
        return [c for c in self.calls if fragment in c.url]

    def rpc_bodies(self, name):
        return [json.loads(c.body) for c in self.to(f"/rpc/{name}")]


class FakeRequest:
    def __init__(self, url, method="GET", headers=None, body=b""):
        self.url = url
        self.method = method
        self.headers = headers or {}
        self._body = body

    async def bytes(self):
        return self._body


class FakeCtx:
    def __init__(self):
        self.pending = []

    def waitUntil(self, awaitable):
        self.pending.append(awaitable)

    async def drain(self):
        while self.pending:
            await self.pending.pop(0)


def make_env(configured=True):
    if not configured:
        return types.SimpleNamespace()
    return types.SimpleNamespace(SUPABASE_URL=SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY="service-key")


class WorkerTestCase(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.fetch = FetchRecorder()
        patcher = mock.patch.object(worker, "fetch", self.fetch)
        patcher.start()
        self.addCleanup(patcher.stop)
        # The handlers print diagnostics; keep test output quiet.
        print_patcher = mock.patch("builtins.print")
        print_patcher.start()
        self.addCleanup(print_patcher.stop)
        worker._api_token_cache.update(value=None, expires_at=0.0)
        self.addCleanup(worker._api_token_cache.update, value=None, expires_at=0.0)
        self.env = make_env()
        self.ctx = FakeCtx()

    async def dispatch(self, request):
        entry = worker.Default()
        entry.env = self.env
        entry.ctx = self.ctx
        response = await entry.fetch(request)
        await self.ctx.drain()
        return response


class TestRouting(WorkerTestCase):
    async def test_preflight_echoes_requested_headers_for_allowed_origin(self):
        response = await self.dispatch(
            FakeRequest(
                "https://metrics-api.jbirdkerr.net/i/v0/e/",
                method="OPTIONS",
                headers={
                    "Origin": "https://boopmaster3000.jbirdkerr.net",
                    "Access-Control-Request-Headers": "content-type,x-posthog-sdk",
                },
            )
        )
        self.assertEqual(response.status, 204)
        self.assertEqual(
            response.headers["Access-Control-Allow-Origin"], "https://boopmaster3000.jbirdkerr.net"
        )
        self.assertEqual(
            response.headers["Access-Control-Allow-Headers"], "content-type,x-posthog-sdk"
        )
        self.assertEqual(self.fetch.calls, [], "preflight must not hit upstream services")

    async def test_preflight_from_unknown_origin_gets_no_allow_origin(self):
        response = await self.dispatch(
            FakeRequest(
                "https://metrics-api.jbirdkerr.net/capture",
                method="OPTIONS",
                headers={"Origin": "https://evil.example"},
            )
        )
        self.assertEqual(response.status, 204)
        self.assertNotIn("Access-Control-Allow-Origin", response.headers)

    async def test_health_returns_ok_and_records_health(self):
        response = await self.dispatch(FakeRequest("https://metrics-api.jbirdkerr.net/health"))
        self.assertEqual(response.status, 200)
        self.assertEqual(json.loads(response.body)["status"], "ok")
        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertEqual(health["p_request_type"], "health")
        self.assertEqual(health["p_status_code"], 200)
        self.assertIn("p_latency_ms", health)

    async def test_unknown_path_returns_404_and_records_not_found(self):
        response = await self.dispatch(FakeRequest("https://metrics-api.jbirdkerr.net/nope"))
        self.assertEqual(response.status, 404)
        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertEqual(health["p_request_type"], "not_found")
        self.assertEqual(health["p_status_code"], 404)

    async def test_metrics_route_records_response_status(self):
        self.fetch.routes = {
            "/rpc/get_site_metrics": FakeFetchResponse(
                json_body=[{"updated_at": "2026-11-20T00:00:00Z"}]
            ),
            "/rpc/get_site_event_counts": FakeFetchResponse(json_body=[]),
        }
        response = await self.dispatch(FakeRequest("https://metrics-api.jbirdkerr.net/metrics"))
        self.assertEqual(response.status, 200)
        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertEqual(health["p_request_type"], "metrics")
        self.assertEqual(health["p_status_code"], 200)

    async def test_health_metrics_route_counts_as_metrics(self):
        # The DB function only knows proxy/metrics/health/not_found, so the
        # dashboard endpoint is deliberately counted as "metrics".
        self.fetch.routes = {"/rpc/get_worker_health_stats": FakeFetchResponse(json_body=[{}])}
        response = await self.dispatch(
            FakeRequest("https://metrics-api.jbirdkerr.net/health-metrics")
        )
        self.assertEqual(response.status, 200)
        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertEqual(health["p_request_type"], "metrics")

    async def test_metrics_post_is_not_routed_to_metrics_handler(self):
        response = await self.dispatch(
            FakeRequest("https://metrics-api.jbirdkerr.net/metrics", method="POST")
        )
        self.assertEqual(response.status, 404)

    async def test_posthog_upstream_5xx_is_flagged_in_health(self):
        self.fetch.routes = {worker.POSTHOG_API_HOST: FakeFetchResponse(status=502)}
        response = await self.dispatch(
            FakeRequest("https://metrics-api.jbirdkerr.net/decide", method="GET")
        )
        self.assertEqual(response.status, 502)
        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertEqual(health["p_request_type"], "proxy")
        self.assertTrue(health["p_posthog_proxy_failed"])

    async def test_posthog_upstream_4xx_is_not_flagged_as_proxy_failure(self):
        self.fetch.routes = {worker.POSTHOG_API_HOST: FakeFetchResponse(status=400)}
        await self.dispatch(FakeRequest("https://metrics-api.jbirdkerr.net/decide"))
        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertNotIn("p_posthog_proxy_failed", health)


class TestPostHogProxy(WorkerTestCase):
    async def proxy(self, request):
        url = worker.urlparse(request.url)
        cors = worker.get_cors_headers(request.headers.get("Origin"))
        response = await worker.proxy_posthog(request, url, self.env, cors, self.ctx)
        await self.ctx.drain()
        return response

    async def test_assets_go_to_assets_host_with_public_cors_and_caching(self):
        self.fetch.routes = {
            worker.POSTHOG_ASSETS_HOST: FakeFetchResponse(
                body=b"js",
                headers={
                    "Content-Type": "application/javascript",
                    "Content-Encoding": "br",
                    "Content-Length": "2",
                    "Access-Control-Allow-Origin": "https://us.posthog.com",
                },
            )
        }
        response = await self.proxy(
            FakeRequest("https://metrics-api.jbirdkerr.net/static/array.js?v=1")
        )
        [call] = self.fetch.calls
        self.assertEqual(call.url, f"{worker.POSTHOG_ASSETS_HOST}/static/array.js?v=1")
        self.assertEqual(response.headers["Access-Control-Allow-Origin"], "*")
        self.assertEqual(response.headers["Cache-Control"], "public, max-age=86400")
        self.assertEqual(response.headers["Content-Type"], "application/javascript")
        # Hop-by-hop/encoding headers from upstream must not leak through.
        self.assertNotIn("Content-Encoding", response.headers)
        self.assertNotIn("Content-Length", response.headers)

    async def test_capture_forwards_body_strips_headers_and_persists_events(self):
        events = [{"event": "snoot_boop", "properties": {"combo": 3}, "distinct_id": "u1"}]
        body = json.dumps(events).encode()
        response = await self.proxy(
            FakeRequest(
                "https://metrics-api.jbirdkerr.net/i/v0/e/?compression=none",
                method="POST",
                headers={
                    "Origin": "https://boopmaster3000.jbirdkerr.net",
                    "host": "metrics-api.jbirdkerr.net",
                    "origin": "https://boopmaster3000.jbirdkerr.net",
                    "content-encoding": "gzip",
                    "content-length": "999",
                    "content-type": "application/json",
                },
                body=body,
            )
        )
        self.assertEqual(response.status, 200)
        self.assertEqual(
            response.headers["Access-Control-Allow-Origin"], "https://boopmaster3000.jbirdkerr.net"
        )

        [upstream] = self.fetch.to(worker.POSTHOG_API_HOST)
        self.assertEqual(upstream.url, f"{worker.POSTHOG_API_HOST}/i/v0/e/?compression=none")
        self.assertEqual(upstream.body, body)
        for dropped in ("host", "origin", "content-encoding", "content-length"):
            self.assertNotIn(dropped, upstream.headers)
        self.assertEqual(upstream.headers["content-type"], "application/json")

        [persisted] = self.fetch.rpc_bodies("record_site_events")
        self.assertEqual(persisted["p_events"][0]["event"], "snoot_boop")
        self.assertEqual(persisted["p_events"][0]["distinct_id"], "u1")

    async def test_get_requests_send_no_body_and_are_not_persisted(self):
        await self.proxy(FakeRequest("https://metrics-api.jbirdkerr.net/flags/?v=2"))
        [upstream] = self.fetch.to(worker.POSTHOG_API_HOST)
        self.assertIsNone(upstream.body)
        self.assertEqual(self.fetch.to("record_site_events"), [])

    async def test_session_recordings_are_forwarded_but_not_persisted(self):
        await self.proxy(
            FakeRequest(
                "https://metrics-api.jbirdkerr.net/s/",
                method="POST",
                body=json.dumps({"event": "$snapshot"}).encode(),
            )
        )
        self.assertEqual(len(self.fetch.to(worker.POSTHOG_API_HOST)), 1)
        self.assertEqual(self.fetch.to("record_site_events"), [])


class TestPersistPostHogPayload(WorkerTestCase):
    async def test_missing_configuration_skips_all_writes(self):
        await worker.persist_posthog_payload(b'{"event": "x"}', make_env(configured=False))
        self.assertEqual(self.fetch.calls, [])

    async def test_undecodable_payload_records_decode_failure_only(self):
        await worker.persist_posthog_payload(b"\x00not json", self.env)
        self.assertEqual(self.fetch.to("record_site_events"), [])
        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertFalse(health["p_decode_success"])

    async def test_payload_without_events_writes_nothing(self):
        await worker.persist_posthog_payload(b'{"batch": []}', self.env)
        self.assertEqual(self.fetch.to("record_site_events"), [])
        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertTrue(health["p_decode_success"])

    async def test_events_are_written_to_the_metrics_schema(self):
        await worker.persist_posthog_payload(
            json.dumps({"batch": [{"event": "a"}, {"event": "b"}]}).encode(), self.env
        )
        [write] = self.fetch.to("/rpc/record_site_events")
        self.assertEqual(write.url, f"{SUPABASE_URL}/rest/v1/rpc/record_site_events")
        self.assertEqual(write.method, "POST")
        self.assertEqual(write.headers["Content-Profile"], worker.DB_SCHEMA)
        self.assertEqual([e["event"] for e in json.loads(write.body)["p_events"]], ["a", "b"])

        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertTrue(health["p_decode_success"])
        self.assertIn("p_persist_latency_ms", health)
        self.assertNotIn("p_supabase_write_failed", health)

    async def test_supabase_error_response_is_recorded_as_write_failure(self):
        self.fetch.routes = {"/rpc/record_site_events": FakeFetchResponse(status=500, body=b"boom")}
        await worker.persist_posthog_payload(b'{"event": "a"}', self.env)
        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertTrue(health["p_supabase_write_failed"])

    async def test_supabase_exception_is_recorded_as_write_failure(self):
        self.fetch.routes = {"/rpc/record_site_events": ConnectionError("unreachable")}
        await worker.persist_posthog_payload(b'{"event": "a"}', self.env)
        [health] = self.fetch.rpc_bodies("record_worker_health")
        self.assertTrue(health["p_supabase_write_failed"])


class TestMetricsHandlers(WorkerTestCase):
    async def test_metrics_without_configuration_returns_500(self):
        response = await worker.handle_metrics(make_env(configured=False), {})
        self.assertEqual(response.status, 500)
        self.assertEqual(self.fetch.calls, [])

    async def test_metrics_renders_html_from_the_metrics_schema(self):
        self.fetch.routes = {
            "/rpc/get_site_metrics": FakeFetchResponse(
                json_body=[{"total_events": 42, "updated_at": "2026-11-20T01:02:03Z"}]
            ),
            "/rpc/get_site_event_counts": FakeFetchResponse(
                json_body=[{"event": "boop", "count": 7}]
            ),
        }
        response = await worker.handle_metrics(self.env, {"Vary": "Origin"})
        self.assertEqual(response.status, 200)
        self.assertEqual(response.headers["Content-Type"], "text/html; charset=utf-8")
        self.assertEqual(response.headers["X-Metrics-Generated"], "2026-11-20T01:02:03Z")
        self.assertEqual(response.headers["Vary"], "Origin")
        self.assertIn("boop", response.body)
        [events] = self.fetch.rpc_bodies("get_site_event_counts")
        self.assertEqual(events, {"p_limit": 30})
        for call in self.fetch.calls:
            self.assertEqual(call.method, "POST")
            self.assertEqual(call.headers["Content-Profile"], worker.DB_SCHEMA)

    async def test_metrics_uses_the_appops_token_when_available(self):
        secret = mock.AsyncMock()
        secret.get.return_value = "app-token"
        env = make_env()
        env.SUPABASE_PUBLISHABLE_KEY = "publishable-key"
        env.SUPABASE_API_TOKEN = secret
        self.fetch.routes = {
            "/rpc/get_site_metrics": FakeFetchResponse(
                json_body=[{"updated_at": "2026-11-20T00:00:00Z"}]
            ),
            "/rpc/get_site_event_counts": FakeFetchResponse(json_body=[]),
        }
        response = await worker.handle_metrics(env, {})
        self.assertEqual(response.status, 200)
        self.assertEqual(len(self.fetch.calls), 2)
        for call in self.fetch.calls:
            self.assertEqual(call.headers["apikey"], "publishable-key")
            self.assertEqual(call.headers["Authorization"], "Bearer app-token")
        secret.get.assert_awaited_once()

    async def test_metrics_upstream_failure_returns_503_uncached(self):
        self.fetch.routes = {
            "/rpc/get_site_metrics": FakeFetchResponse(status=500),
            "/rpc/get_site_event_counts": FakeFetchResponse(json_body=[]),
        }
        response = await worker.handle_metrics(self.env, {})
        self.assertEqual(response.status, 503)
        self.assertEqual(response.headers["Cache-Control"], "no-store")

    async def test_health_metrics_without_configuration_returns_500(self):
        response = await worker.handle_health_metrics(make_env(configured=False), {})
        self.assertEqual(response.status, 500)

    async def test_health_metrics_upstream_exception_returns_503(self):
        self.fetch.routes = {"/rpc/get_worker_health_stats": ConnectionError("down")}
        response = await worker.handle_health_metrics(self.env, {})
        self.assertEqual(response.status, 503)
        self.assertEqual(response.headers["Cache-Control"], "no-store")


class TestRecordWorkerHealth(WorkerTestCase):
    async def test_missing_configuration_is_a_no_op(self):
        await worker.record_worker_health(make_env(configured=False), "health", 200)
        self.assertEqual(self.fetch.calls, [])

    async def test_only_set_fields_are_sent(self):
        await worker.record_worker_health(self.env, "proxy", 200, latency_ms=12.7)
        [body] = self.fetch.rpc_bodies("record_worker_health")
        self.assertEqual(
            body, {"p_request_type": "proxy", "p_status_code": 200, "p_latency_ms": 12}
        )

    async def test_fetch_errors_are_swallowed(self):
        self.fetch.routes = {"/rpc/record_worker_health": ConnectionError("down")}
        # Must not raise: health recording is fire-and-forget.
        await worker.record_worker_health(self.env, "health", 200)


if __name__ == "__main__":
    asyncio.run(unittest.main())
