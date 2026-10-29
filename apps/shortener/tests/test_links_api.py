from __future__ import annotations

import pytest

OWNER_B = "22222222-2222-2222-2222-222222222222"


def create(h, target="https://example.com/page", slug=None, key="sk_primary"):
    body = {"target": target}
    if slug is not None:
        body["slug"] = slug
    return h.client.post("/api/links", json=body, headers=h.auth(key))


def test_create_returns_working_short_url(h):
    r = create(h)
    assert r.status_code == 201
    data = r.json()
    assert data["short_url"] == f"https://go.jbirdkerr.net/{data['slug']}"
    assert 6 <= len(data["slug"]) <= 8
    assert data["target"] == "https://example.com/page"
    assert h.client.get(f"/{data['slug']}").status_code == 302


def test_custom_slug_and_conflict(h):
    assert create(h, slug="my-link").status_code == 201
    r = create(h, slug="my-link")
    assert r.status_code == 409 and r.json()["error"]["code"] == "slug_taken"
    # Confusable casing is also taken
    assert create(h, slug="My-Link").status_code == 409


def test_same_target_twice_creates_new_slugs(h):
    a = create(h).json()["slug"]
    b = create(h).json()["slug"]
    assert a != b


@pytest.mark.parametrize(
    "slug", ["api", "health", "static", "login", "LOGOUT", "auth", "ab", "-bad", "bad-", "no_underscore", "a" * 33]
)
def test_rejects_reserved_or_malformed_custom_slugs(h, slug):
    assert create(h, slug=slug).status_code == 422


@pytest.mark.parametrize(
    "target, code",
    [
        ("javascript:alert(1)", "unsupported_scheme"),
        ("data:text/html,hi", "unsupported_scheme"),
        ("file:///etc/passwd", "unsupported_scheme"),
        ("ftp://example.com/x", "unsupported_scheme"),
        ("//example.com/x", "unsupported_scheme"),
        ("example.com/no-scheme", "unsupported_scheme"),
        ("https://go.jbirdkerr.net/abc", "redirect_loop"),
        ("https://GO.jbirdkerr.net./abc", "redirect_loop"),
        ("https://user:pw@example.com/", "invalid_target"),
        ("https://example.com/with space", "invalid_target"),
        ("https://example.com\\@go.jbirdkerr.net", "invalid_target"),
        ("https://", "invalid_target"),
        ("https://example.com:notaport/", "invalid_target"),
        ("https://evil.example/x", "blocked_target"),
        ("https://sub.evil.example/x", "blocked_target"),
        ("", "invalid_target"),
        ("https://example.com/" + "a" * 2100, "invalid_target"),
    ],
)
def test_rejects_bad_targets(h, target, code):
    r = create(h, target=target)
    assert r.status_code == 422
    assert r.json()["error"]["code"] == code


def test_allows_similar_but_distinct_host(h):
    assert create(h, target="https://notevil.example/x").status_code == 201


def test_protected_endpoints_require_a_valid_key(h):
    calls = [
        ("post", "/api/links", {"json": {"target": "https://example.com"}}),
        ("get", "/api/links/abc", {}),
        ("get", "/api/links/abc/stats", {}),
        ("delete", "/api/links/abc", {}),
    ]
    for method, path, kwargs in calls:
        for headers in ({}, {"Authorization": "Bearer nope"}, {"Authorization": "Basic abc"}):
            r = getattr(h.client, method)(path, headers=headers, **kwargs)
            assert r.status_code == 401, (method, path, headers)
            assert r.headers["www-authenticate"] == "Bearer"


def test_creation_is_rate_limited_per_key(h):
    for _ in range(3):
        assert create(h).status_code == 201
    r = create(h)
    assert r.status_code == 429
    assert r.headers["retry-after"] == "1234"
    # Another key has its own budget
    assert create(h, key="sk_other").status_code == 201


def test_metadata_and_owner_scoping(h):
    slug = create(h).json()["slug"]
    r = h.client.get(f"/api/links/{slug}", headers=h.auth())
    assert r.status_code == 200
    assert r.json()["disabled"] is False
    # Another owner's key sees a 404, same as a missing slug
    assert h.client.get(f"/api/links/{slug}", headers=h.auth("sk_other")).status_code == 404
    assert h.client.get("/api/links/does-not-exist", headers=h.auth()).status_code == 404


def test_delete_disables_and_never_frees_the_slug(h):
    slug = create(h, slug="gone-soon").json()["slug"]
    assert h.client.delete(f"/api/links/{slug}", headers=h.auth("sk_other")).status_code == 404
    assert h.client.get(f"/{slug}").status_code == 302

    r = h.client.delete(f"/api/links/{slug}", headers=h.auth())
    assert r.status_code == 200 and r.json() == {"slug": slug, "disabled": True}
    assert h.client.get(f"/{slug}").status_code == 404
    assert h.client.get(f"/api/links/{slug}", headers=h.auth()).json()["disabled"] is True
    assert create(h, slug=slug).status_code == 409


def test_health_makes_no_database_calls(h):
    assert h.client.get("/health").json() == {"status": "ok"}
    assert h.store.calls == []
