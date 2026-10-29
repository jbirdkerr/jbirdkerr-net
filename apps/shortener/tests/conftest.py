from __future__ import annotations

import asyncio
from collections import Counter
from dataclasses import dataclass, field
from datetime import date, datetime, timedelta, timezone

import pytest
from fastapi.testclient import TestClient

from shortener.app import create_app
from shortener.auth import hash_key
from shortener.config import Settings
from shortener.context import AppContext
from shortener.links import ConfigBlocklist
from shortener.models import ApiKey, Link, RateLimitResult

OWNER_A = "11111111-1111-1111-1111-111111111111"
OWNER_B = "22222222-2222-2222-2222-222222222222"
NOW = datetime(2026, 10, 28, 15, 30, tzinfo=timezone.utc)


class FakeStore:
    """In-memory stand-in for SupabaseStore that mimics the SQL behavior."""

    def __init__(self) -> None:
        self.keys: dict[str, ApiKey] = {}
        self.links: dict[str, Link] = {}
        self.clicks: Counter[tuple[str, date, str, str]] = Counter()
        self.rate_hits: Counter[str] = Counter()
        self.calls: list[str] = []

    async def get_api_key(self, key_hash):
        self.calls.append("get_api_key")
        return self.keys.get(key_hash)

    async def check_rate_limit(self, key_hash, limit, window_seconds):
        self.calls.append("check_rate_limit")
        self.rate_hits[key_hash] += 1
        return RateLimitResult(self.rate_hits[key_hash] <= limit, 1234)

    async def create_link(self, slug, target, owner_id):
        self.calls.append("create_link")
        if slug.lower() in {s.lower() for s in self.links}:
            return None
        link = Link(slug, target, owner_id, NOW, False)
        self.links[slug] = link
        return link

    async def get_link(self, slug):
        self.calls.append("get_link")
        return self.links.get(slug)

    async def disable_link(self, slug):
        self.calls.append("disable_link")
        link = self.links[slug]
        self.links[slug] = Link(link.slug, link.target, link.owner_id, link.created_at, True)

    async def record_click(self, slug, day, country, referrer_host):
        self.calls.append("record_click")
        self.clicks[(slug, day, country, referrer_host)] += 1

    async def link_stats(self, slug, from_day, to_day, top_n):
        self.calls.append("link_stats")
        rows = [(k, v) for k, v in self.clicks.items() if k[0] == slug]
        in_range = [(k, v) for k, v in rows if from_day <= k[1] <= to_day]

        def top(index, name):
            totals: Counter[str] = Counter()
            for k, v in in_range:
                totals[k[index]] += v
            ordered = sorted(totals.items(), key=lambda kv: (-kv[1], kv[0]))[:top_n]
            return [{name: key, "clicks": count} for key, count in ordered]

        per_day: Counter[date] = Counter()
        for k, v in in_range:
            per_day[k[1]] += v
        return {
            "total_clicks": sum(v for _, v in rows),
            "clicks_in_range": sum(v for _, v in in_range),
            "daily": [{"day": d.isoformat(), "clicks": c} for d, c in sorted(per_day.items())],
            "top_referrers": top(3, "host"),
            "top_countries": top(2, "country"),
        }


@dataclass
class Harness:
    client: TestClient
    store: FakeStore
    ctx: AppContext
    pending: list = field(default_factory=list)

    def run_background(self) -> None:
        """Run the work the app scheduled to happen after the response."""
        async def _run(coros):
            await asyncio.gather(*coros)

        coros, self.pending[:] = list(self.pending), []
        asyncio.run(_run(coros))

    def auth(self, raw_key: str = "sk_primary") -> dict[str, str]:
        return {"Authorization": f"Bearer {raw_key}"}


@pytest.fixture
def h():
    store = FakeStore()
    store.keys[hash_key("sk_primary")] = ApiKey(hash_key("sk_primary"), "primary", OWNER_A)
    store.keys[hash_key("sk_other")] = ApiKey(hash_key("sk_other"), "other", OWNER_B)

    pending: list = []
    settings = Settings(
        supabase_url="https://example.supabase.co",
        supabase_service_key="test",
        blocked_hosts=frozenset({"evil.example"}),
        create_rate_limit=3,
    )
    ctx = AppContext(
        settings=settings,
        store=store,
        blocklist=ConfigBlocklist(settings.blocked_hosts),
        schedule=pending.append,
        now=lambda: NOW,
    )
    app = create_app()
    app.state.ctx = ctx
    yield Harness(TestClient(app, follow_redirects=False), store, ctx, pending)
    for coro in pending:  # scheduled but intentionally never run in this test
        coro.close()


def day(offset: int = 0) -> date:
    return NOW.date() + timedelta(days=offset)
