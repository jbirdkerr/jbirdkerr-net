from __future__ import annotations

from datetime import date

import pytest

from shortener.errors import ApiError
from shortener.stats import parse_range

TODAY = date(2026, 10, 28)


def seed(h, slug="abc123"):
    h.client.post("/api/links", json={"target": "https://example.com", "slug": slug}, headers=h.auth())
    clicks = h.store.clicks
    clicks[(slug, date(2026, 10, 28), "US", "direct")] += 4
    clicks[(slug, date(2026, 10, 28), "DE", "news.example.org")] += 2
    clicks[(slug, date(2026, 10, 26), "US", "news.example.org")] += 3
    clicks[(slug, date(2026, 9, 1), "US", "direct")] += 10  # outside a 7d range
    return slug


def test_stats_are_zero_filled_with_correct_totals(h):
    slug = seed(h)
    r = h.client.get(f"/api/links/{slug}/stats?range=7d", headers=h.auth())
    assert r.status_code == 200
    s = r.json()
    assert s["slug"] == slug and s["range"] == "7d"
    assert s["total_clicks"] == 19
    assert s["clicks_in_range"] == 9
    assert [d["day"] for d in s["daily"]] == [f"2026-10-{n}" for n in range(22, 29)]
    assert [d["clicks"] for d in s["daily"]] == [0, 0, 0, 0, 3, 0, 6]
    assert s["top_referrers"] == [
        {"host": "news.example.org", "clicks": 5},
        {"host": "direct", "clicks": 4},
    ]
    assert s["top_countries"] == [{"country": "US", "clicks": 7}, {"country": "DE", "clicks": 2}]


def test_default_range_is_7d_and_24h_means_today(h):
    slug = seed(h)
    assert len(h.client.get(f"/api/links/{slug}/stats", headers=h.auth()).json()["daily"]) == 7
    s = h.client.get(f"/api/links/{slug}/stats?range=24h", headers=h.auth()).json()
    assert [d["day"] for d in s["daily"]] == ["2026-10-28"] and s["clicks_in_range"] == 6


def test_stats_for_a_link_with_no_clicks(h):
    h.client.post("/api/links", json={"target": "https://example.com", "slug": "quiet-one"}, headers=h.auth())
    s = h.client.get("/api/links/quiet-one/stats?range=30d", headers=h.auth()).json()
    assert s["total_clicks"] == 0 and len(s["daily"]) == 30
    assert all(d["clicks"] == 0 for d in s["daily"])
    assert s["top_referrers"] == [] and s["top_countries"] == []


def test_other_owners_stats_are_404(h):
    slug = seed(h)
    assert h.client.get(f"/api/links/{slug}/stats", headers=h.auth("sk_other")).status_code == 404


@pytest.mark.parametrize("value", ["", "7", "d7", "0d", "-1d", "91d", "1.5d", "7w", "9999d"])
def test_invalid_ranges_are_422(h, value):
    slug = seed(h)
    assert h.client.get(f"/api/links/{slug}/stats?range={value}", headers=h.auth()).status_code == 422


@pytest.mark.parametrize(
    "value, days", [("24h", 1), ("25h", 2), ("48h", 2), ("7d", 7), ("30D", 30), ("90d", 90)]
)
def test_parse_range(value, days):
    assert parse_range(value, 90).days == days


def test_parse_range_rejects_garbage():
    with pytest.raises(ApiError):
        parse_range("soon", 90)
