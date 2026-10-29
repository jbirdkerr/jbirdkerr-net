from __future__ import annotations

from datetime import date


def make_link(h, slug="abc123", target="https://example.com/dest?x=1"):
    r = h.client.post(
        "/api/links", json={"target": target, "slug": slug}, headers=h.auth()
    )
    assert r.status_code == 201
    return slug


def test_redirect_is_302_no_store_and_does_not_wait_for_the_write(h):
    make_link(h)
    r = h.client.get("/abc123")
    assert r.status_code == 302
    assert r.headers["location"] == "https://example.com/dest?x=1"
    assert r.headers["cache-control"] == "no-store"

    # The response is out, and the click write is only scheduled, not done.
    assert len(h.pending) == 1
    assert not h.store.clicks
    h.run_background()
    assert h.store.clicks[("abc123", date(2026, 10, 28), "unknown", "direct")] == 1


def test_records_country_and_referrer_host_only(h):
    make_link(h)
    h.client.get(
        "/abc123",
        headers={
            "cf-ipcountry": "de",
            "referer": "https://News.Example.org/some/path?utm=secret#frag",
            "user-agent": "Mozilla/5.0 (secret)",
            "cookie": "a=b",
        },
    )
    h.run_background()
    assert list(h.store.clicks) == [("abc123", date(2026, 10, 28), "DE", "news.example.org")]

import pytest


@pytest.mark.parametrize("country", ["XX", "T1", "", "USA", "1A"])
def test_unusable_country_values_become_unknown(h, country):
    make_link(h)
    h.client.get("/abc123", headers={"cf-ipcountry": country})
    h.run_background()
    assert list(h.store.clicks)[0][2] == "unknown"


def test_repeat_clicks_increment_the_same_row(h):
    make_link(h)
    for _ in range(3):
        h.client.get("/abc123")
    h.run_background()
    assert sum(h.store.clicks.values()) == 3 and len(h.store.clicks) == 1


def test_unknown_and_disabled_slugs_are_404_and_record_nothing(h):
    make_link(h)
    h.client.delete("/api/links/abc123", headers=h.auth())
    for slug in ("abc123", "nope-nope"):
        assert h.client.get(f"/{slug}").status_code == 404
    assert not h.pending


def test_slugs_that_cannot_exist_skip_the_database(h):
    for path in ("/favicon.ico", "/a", "/robots.txt", "/" + "x" * 40):
        assert h.client.get(path).status_code == 404
    assert h.store.calls == []


def test_head_redirects_but_is_not_counted(h):
    make_link(h)
    r = h.client.head("/abc123")
    assert r.status_code == 302
    assert not h.pending


def test_a_failing_click_write_does_not_raise(h, capsys):
    make_link(h)

    async def boom(*args, **kwargs):
        raise RuntimeError("db down")

    h.store.record_click = boom
    assert h.client.get("/abc123").status_code == 302
    h.run_background()  # must not raise
    assert "record_click failed" in capsys.readouterr().out
