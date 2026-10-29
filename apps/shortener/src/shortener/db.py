"""Supabase access through PostgREST. Everything lives in the `shortener` schema.

Note: the schema must be listed under "Exposed schemas" in the Supabase API settings
for PostgREST to serve it (see README).
"""
from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import date, datetime
from typing import Any, Protocol
from urllib.parse import quote

from .config import Settings
from .errors import StoreError
from .models import ApiKey, Link, RateLimitResult

SCHEMA = "shortener"


@dataclass
class HttpResponse:
    status: int
    body: Any


class Transport(Protocol):
    async def request(
        self, method: str, url: str, *, headers: dict[str, str], payload: Any = None
    ) -> HttpResponse: ...


class FetchTransport:
    """HTTP via the Workers runtime's fetch, exposed to Python by Pyodide."""

    async def request(
        self, method: str, url: str, *, headers: dict[str, str], payload: Any = None
    ) -> HttpResponse:
        from pyodide.http import pyfetch  # pants: no-infer-dep

        kwargs: dict[str, Any] = {"method": method, "headers": headers}
        if payload is not None:
            kwargs["body"] = json.dumps(payload)
        response = await pyfetch(url, **kwargs)
        text = await response.string()
        try:
            body = json.loads(text) if text else None
        except ValueError:
            body = text
        return HttpResponse(status=response.status, body=body)


class Store(Protocol):
    async def get_api_key(self, key_hash: str) -> ApiKey | None: ...
    async def check_rate_limit(self, key_hash: str, limit: int, window_seconds: int) -> RateLimitResult: ...
    async def create_link(self, slug: str, target: str, owner_id: str) -> Link | None: ...
    async def get_link(self, slug: str) -> Link | None: ...
    async def disable_link(self, slug: str) -> None: ...
    async def record_click(self, slug: str, day: date, country: str, referrer_host: str) -> None: ...
    async def link_stats(self, slug: str, from_day: date, to_day: date, top_n: int) -> dict[str, Any]: ...


def _link_from_row(row: dict[str, Any]) -> Link:
    return Link(
        slug=row["slug"],
        target=row["target"],
        owner_id=row["owner_id"],
        created_at=datetime.fromisoformat(row["created_at"]),
        disabled=bool(row["disabled"]),
    )


class SupabaseStore:
    def __init__(self, settings: Settings, transport: Transport) -> None:
        self._base = f"{settings.supabase_url}/rest/v1"
        self._transport = transport
        self._headers = {
            "apikey": settings.supabase_service_key,
            "Authorization": f"Bearer {settings.supabase_service_key}",
            "Accept-Profile": SCHEMA,
            "Content-Profile": SCHEMA,
            "Content-Type": "application/json",
        }

    async def _call(
        self,
        method: str,
        path: str,
        *,
        payload: Any = None,
        prefer: str | None = None,
        ok: tuple[int, ...] = (200, 201, 204),
    ) -> HttpResponse:
        headers = dict(self._headers)
        if prefer:
            headers["Prefer"] = prefer
        try:
            response = await self._transport.request(
                method, f"{self._base}{path}", headers=headers, payload=payload
            )
        except Exception as exc:  # network-level failure
            raise StoreError(f"{method} {path.split('?')[0]} failed: {exc!r}") from exc
        if response.status not in ok:
            raise StoreError(f"{method} {path.split('?')[0]} returned {response.status}")
        return response

    async def get_api_key(self, key_hash: str) -> ApiKey | None:
        response = await self._call(
            "GET",
            f"/api_keys?select=key_hash,label,owner_id&key_hash=eq.{quote(key_hash, safe='')}"
            "&revoked=eq.false&limit=1",
        )
        rows = response.body or []
        if not rows:
            return None
        row = rows[0]
        return ApiKey(key_hash=row["key_hash"], label=row["label"], owner_id=row["owner_id"])

    async def check_rate_limit(self, key_hash: str, limit: int, window_seconds: int) -> RateLimitResult:
        response = await self._call(
            "POST",
            "/rpc/check_rate_limit",
            payload={"p_key_hash": key_hash, "p_limit": limit, "p_window_seconds": window_seconds},
        )
        rows = response.body or []
        if not rows:
            raise StoreError("check_rate_limit returned no rows")
        return RateLimitResult(allowed=bool(rows[0]["allowed"]), retry_after=int(rows[0]["retry_after"]))

    async def create_link(self, slug: str, target: str, owner_id: str) -> Link | None:
        """Insert a link. Returns None if the slug is already taken."""
        response = await self._call(
            "POST",
            "/links",
            payload={"slug": slug, "target": target, "owner_id": owner_id},
            prefer="return=representation",
            ok=(200, 201, 409),
        )
        if response.status == 409:
            return None
        rows = response.body or []
        if not rows:
            raise StoreError("insert into links returned no row")
        return _link_from_row(rows[0])

    async def get_link(self, slug: str) -> Link | None:
        response = await self._call(
            "GET",
            f"/links?select=slug,target,owner_id,created_at,disabled&slug=eq.{quote(slug, safe='')}&limit=1",
        )
        rows = response.body or []
        return _link_from_row(rows[0]) if rows else None

    async def disable_link(self, slug: str) -> None:
        await self._call(
            "PATCH",
            f"/links?slug=eq.{quote(slug, safe='')}",
            payload={"disabled": True},
            prefer="return=minimal",
        )

    async def record_click(self, slug: str, day: date, country: str, referrer_host: str) -> None:
        # Exactly these four values are written. Nothing else about the visitor is sent.
        await self._call(
            "POST",
            "/rpc/record_click",
            payload={
                "p_slug": slug,
                "p_day": day.isoformat(),
                "p_country": country,
                "p_referrer_host": referrer_host,
            },
        )

    async def link_stats(self, slug: str, from_day: date, to_day: date, top_n: int) -> dict[str, Any]:
        response = await self._call(
            "POST",
            "/rpc/link_stats",
            payload={
                "p_slug": slug,
                "p_from": from_day.isoformat(),
                "p_to": to_day.isoformat(),
                "p_top_n": top_n,
            },
        )
        return response.body or {}
