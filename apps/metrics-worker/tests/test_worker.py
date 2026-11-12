import base64
import gzip
import json
import types
import unittest

from worker import (
    decode_posthog_body,
    escape_html,
    fmt,
    get_cors_headers,
    is_posthog_path,
    normalize_posthog_payload,
    number,
    render_health_metrics_html,
    render_metrics_html,
    supabase_headers,
)


class TestMetricsWorker(unittest.TestCase):
    def test_decode_posthog_body_direct_json(self):
        data = {"event": "snoot_boop", "properties": {"combo": 5}}
        raw_bytes = json.dumps(data).encode("utf-8")
        result = decode_posthog_body(raw_bytes)
        self.assertEqual(result, data)

    def test_decode_posthog_body_gzip(self):
        data = {"event": "snoot_boop", "properties": {"combo": 10}}
        raw_bytes = gzip.compress(json.dumps(data).encode("utf-8"))
        result = decode_posthog_body(raw_bytes)
        self.assertEqual(result, data)

    def test_decode_posthog_body_base64_data_param(self):
        data = {"event": "party_mode_toggled", "properties": {"enabled": True}}
        b64_str = base64.b64encode(json.dumps(data).encode("utf-8")).decode("ascii")
        raw_bytes = f"data={b64_str}".encode()
        result = decode_posthog_body(raw_bytes)
        self.assertEqual(result, data)

    def test_decode_posthog_body_base64_gzipped(self):
        data = {"event": "treat_showered", "properties": {"count": 42}}
        gzipped = gzip.compress(json.dumps(data).encode("utf-8"))
        b64_str = base64.b64encode(gzipped).decode("ascii")
        raw_bytes = f"data={b64_str}".encode()
        result = decode_posthog_body(raw_bytes)
        self.assertEqual(result, data)

    def test_decode_posthog_body_query_string(self):
        data = {"event": "query_event"}
        b64_str = base64.b64encode(json.dumps(data).encode("utf-8")).decode("ascii")
        result = decode_posthog_body(b"", query_string=f"data={b64_str}")
        self.assertEqual(result, data)

    def test_decode_posthog_body_invalid(self):
        self.assertIsNone(decode_posthog_body(b"not-json-or-base64"))
        self.assertIsNone(decode_posthog_body(None))

    def test_normalize_posthog_payload_batch(self):
        payload = {
            "batch": [
                {
                    "event": "snoot_boop",
                    "properties": {"combo": 1},
                    "timestamp": "2026-10-02T00:00:00Z",
                    "distinct_id": "user_123",
                },
                {"event": "$pageview", "properties": {}},
            ]
        }
        normalized = normalize_posthog_payload(payload)
        self.assertEqual(len(normalized), 2)
        self.assertEqual(normalized[0]["event"], "snoot_boop")
        self.assertEqual(normalized[0]["distinct_id"], "user_123")
        self.assertEqual(normalized[1]["event"], "$pageview")

    def test_normalize_posthog_payload_single(self):
        payload = {
            "event": "googly_eyes_toggled",
            "properties": {"enabled": True},
            "timestamp": "2026-10-02T00:00:00Z",
        }
        normalized = normalize_posthog_payload(payload)
        self.assertEqual(len(normalized), 1)
        self.assertEqual(normalized[0]["event"], "googly_eyes_toggled")

    def test_normalize_posthog_payload_invalid(self):
        self.assertEqual(normalize_posthog_payload(None), [])
        self.assertEqual(normalize_posthog_payload("invalid string"), [])
        self.assertEqual(normalize_posthog_payload([{"invalid": "no event key"}]), [])

    def test_escape_html(self):
        raw = "<script>alert(\"xss\" & 'test')</script>"
        escaped = escape_html(raw)
        self.assertNotIn("<script>", escaped)
        self.assertIn("&lt;script&gt;", escaped)
        self.assertIn("&amp;", escaped)
        self.assertIn("&quot;", escaped)
        self.assertIn("&#039;", escaped)

    def test_number_and_fmt(self):
        self.assertEqual(number("123.45"), 123.45)
        self.assertEqual(number(None), 0)
        self.assertEqual(number("invalid"), 0)
        self.assertEqual(fmt(1000), "1,000")
        self.assertEqual(fmt(1234.56), "1,234.56")

    def test_is_posthog_path(self):
        self.assertTrue(is_posthog_path("/i/v0/e/"))
        self.assertTrue(is_posthog_path("/static/array.js"))
        self.assertTrue(is_posthog_path("/decide"))
        self.assertTrue(is_posthog_path("/batch"))
        self.assertFalse(is_posthog_path("/metrics"))
        self.assertFalse(is_posthog_path("/health"))
        self.assertFalse(is_posthog_path("/unknown"))

    def test_get_cors_headers(self):
        headers_allowed = get_cors_headers("https://jbirdkerr.net")
        self.assertEqual(
            headers_allowed.get("Access-Control-Allow-Origin"), "https://jbirdkerr.net"
        )
        headers_boopmaster = get_cors_headers("https://boopmaster3000.jbirdkerr.net")
        self.assertEqual(
            headers_boopmaster.get("Access-Control-Allow-Origin"),
            "https://boopmaster3000.jbirdkerr.net",
        )

        headers_disallowed = get_cors_headers("https://malicious-site.example.com")
        self.assertNotIn("Access-Control-Allow-Origin", headers_disallowed)

    def test_render_metrics_html(self):
        summary = {
            "total_boops": 42,
            "max_boop_combo": 7,
            "total_events": 100,
            "eye_tracking_toggles": 5,
            "googly_eyes_toggles": 3,
            "party_mode_toggles": 2,
            "treat_showers": 8,
            "total_eye_distance": 3780,
            "updated_at": "2026-10-02T01:00:00Z",
        }
        event_rows = [
            {"event": "snoot_boop", "count": 42},
            {"event": "$pageview", "count": 999},  # Internal PostHog event
            {"event": "treat_showered", "count": 8},
        ]
        html = render_metrics_html(summary, event_rows)
        self.assertIn("Total Boops", html)
        self.assertIn("42", html)
        self.assertIn("1.00 m", html)  # 3780 / 3780
        self.assertIn("snoot_boop", html)
        self.assertIn("treat_showered", html)
        self.assertNotIn("$pageview", html)  # Should filter internal $ events

    def test_render_health_metrics_html(self):
        stats = {
            "total_requests": 150,
            "total_proxy_requests": 100,
            "total_metrics_requests": 40,
            "total_health_requests": 10,
            "total_errors_4xx": 2,
            "total_errors_5xx": 1,
            "total_supabase_write_failures": 0,
            "total_posthog_proxy_failures": 0,
            "total_decode_failures": 0,
            "total_decode_successes": 100,
            "sum_metrics_latency_ms": 1200,
            "count_metrics_latency": 40,
            "sum_proxy_latency_ms": 5000,
            "count_proxy_latency": 100,
            "sum_persist_latency_ms": 3000,
            "count_persist_latency": 100,
            "updated_at": "2026-10-05T22:00:00Z",
        }
        html = render_health_metrics_html(stats)
        self.assertIn("Request Volume", html)
        self.assertIn("150", html)
        self.assertIn("30 ms", html)  # 1200 / 40 avg metrics
        self.assertIn("50 ms", html)  # 5000 / 100 avg proxy
        self.assertIn("100.0%", html)  # decode success rate
        self.assertIn("Pipeline Health", html)

    def test_supabase_headers_read_uses_accept_profile(self):
        env = types.SimpleNamespace(SUPABASE_SERVICE_ROLE_KEY="key")
        headers = supabase_headers(env)
        self.assertEqual(headers["Accept-Profile"], "metrics_worker")
        self.assertNotIn("Content-Profile", headers)
        self.assertEqual(headers["Authorization"], "Bearer key")

    def test_supabase_headers_write_uses_content_profile(self):
        env = types.SimpleNamespace(SUPABASE_SERVICE_ROLE_KEY="key")
        headers = supabase_headers(env, write=True)
        self.assertEqual(headers["Content-Profile"], "metrics_worker")
        self.assertEqual(headers["Content-Type"], "application/json")
        self.assertNotIn("Accept-Profile", headers)


if __name__ == "__main__":
    unittest.main()
