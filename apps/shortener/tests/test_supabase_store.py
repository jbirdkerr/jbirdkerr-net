from __future__ import annotations

import asyncio
import re
from datetime import date
from pathlib import Path

import pytest

from shortener.config import Settings
from shortener.db import HttpResponse, SupabaseStore
from shortener.errors import StoreError

MIGRATIONS = Path(__file__).resolve().parents[1] / "migrations"


class FakeTransport:
    def __init__(self, *responses):
        self.responses = list(responses)
        self.requests = []

    async def request(self, method, url, *, headers, payload=None):
        self.requests.append((method, url, headers, payload))
        return self.responses.pop(0)


def store_with(*responses):
    transport = FakeTransport(*responses)
    settings = Settings(supabase_url="https://p.supabase.co", supabase_service_key="svc-key")
    return SupabaseStore(settings, transport), transport


LINK_ROW = {
    "slug": "abc123",
    "target": "https://example.com",
    "owner_id": "o1",
    "created_at": "2026-10-28T12:00:00.123456+00:00",
    "disabled": False,
}


def test_requests_target_the_shortener_schema_with_the_service_key():
    store, t = store_with(HttpResponse(200, [LINK_ROW]))
    link = asyncio.run(store.get_link("abc123"))
    method, url, headers, _ = t.requests[0]
    assert method == "GET" and url.startswith("https://p.supabase.co/rest/v1/links?")
    assert "slug=eq.abc123" in url
    assert headers["Accept-Profile"] == headers["Content-Profile"] == "shortener"
    assert headers["apikey"] == "svc-key" and headers["Authorization"] == "Bearer svc-key"
    assert link.created_at.tzinfo is not None and link.disabled is False


def test_record_click_sends_exactly_four_fields_and_nothing_about_the_visitor():
    store, t = store_with(HttpResponse(204, None))
    asyncio.run(store.record_click("abc123", date(2026, 10, 28), "DE", "news.example.org"))
    method, url, _, payload = t.requests[0]
    assert method == "POST" and url.endswith("/rpc/record_click")
    assert payload == {
        "p_slug": "abc123",
        "p_day": "2026-10-28",
        "p_country": "DE",
        "p_referrer_host": "news.example.org",
    }


def test_create_link_conflict_returns_none_and_other_errors_raise():
    store, _ = store_with(HttpResponse(409, {"code": "23505"}))
    assert asyncio.run(store.create_link("abc123", "https://example.com", "o1")) is None
    store, _ = store_with(HttpResponse(500, "boom"))
    with pytest.raises(StoreError):
        asyncio.run(store.create_link("abc123", "https://example.com", "o1"))


def test_get_api_key_filters_revoked_and_handles_missing():
    store, t = store_with(HttpResponse(200, []))
    assert asyncio.run(store.get_api_key("deadbeef")) is None
    assert "revoked=eq.false" in t.requests[0][1]


def test_network_failures_become_store_errors():
    class Down:
        async def request(self, *a, **k):
            raise OSError("unreachable")

    store = SupabaseStore(Settings(supabase_url="https://p.supabase.co", supabase_service_key="k"), Down())
    with pytest.raises(StoreError):
        asyncio.run(store.get_link("abc123"))


def test_schema_has_no_visitor_identifying_columns():
    forbidden = re.compile(r"\b(ip|ip_address|user_agent|ua|cookie|visitor_id|fingerprint)\b", re.I)
    tables = ""
    for path in sorted(MIGRATIONS.glob("*.sql")):
        tables += "\n".join(re.findall(r"CREATE TABLE.*?\n\);", path.read_text(), re.S | re.I))
    assert "click_daily" in tables  # the scan actually saw the tables
    assert not forbidden.search(tables)


def test_migrations_are_timestamped_and_ordered():
    names = [p.name for p in sorted(MIGRATIONS.glob("*.sql"))]
    assert names and all(re.match(r"^\d{14}_[a-z0-9_]+\.sql$", n) for n in names)
