from __future__ import annotations

import pytest

from shortener.auth import hash_key
from shortener.config import Settings
from shortener.links import (
    RESERVED_SLUGS,
    SLUG_ALPHABET,
    ConfigBlocklist,
    country_from_headers,
    generate_slug,
    referrer_host,
)


def test_generated_slugs_are_urlsafe_short_and_not_reserved():
    for _ in range(200):
        slug = generate_slug()
        assert 6 <= len(slug) <= 8 and set(slug) <= set(SLUG_ALPHABET)
        assert slug.lower() not in RESERVED_SLUGS


@pytest.mark.parametrize(
    "value, expected",
    [
        (None, "direct"),
        ("", "direct"),
        ("not a url", "direct"),
        ("https://Example.COM/a/b?c=d", "example.com"),
        ("https://example.com.:8080/x", "example.com"),
        ("android-app://com.google.android.gm", "com.google.android.gm"),
    ],
)
def test_referrer_host(value, expected):
    assert referrer_host(value) == expected


def test_country_header_is_case_insensitive():
    assert country_from_headers({"cf-ipcountry": "gb"}) == "GB"
    assert country_from_headers({}) == "unknown"


def test_blocklist_matches_subdomains_but_not_suffix_lookalikes():
    bl = ConfigBlocklist(frozenset({"bad.test"}))
    assert bl.is_blocked("bad.test") and bl.is_blocked("a.bad.test")
    assert not bl.is_blocked("notbad.test")


def test_hash_key_is_stable_sha256_hex():
    assert hash_key("abc") == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"


def test_settings_from_env_supports_dicts_and_requires_supabase():
    s = Settings.from_env(
        {"SUPABASE_URL": "https://x.supabase.co/", "SUPABASE_SERVICE_KEY": "k", "BLOCKED_HOSTS": " A.test, b.test."}
    )
    assert s.supabase_url == "https://x.supabase.co"
    assert s.blocked_hosts == {"a.test", "b.test"}
    assert s.self_hosts == {"go.jbirdkerr.net"}
    with pytest.raises(RuntimeError):
        Settings.from_env({})
