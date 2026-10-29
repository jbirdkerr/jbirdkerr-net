from __future__ import annotations

from datetime import date
from typing import Any

from fastapi import Depends, FastAPI, Query, Request
from fastapi.responses import JSONResponse, RedirectResponse
from pydantic import BaseModel, ConfigDict

from .auth import require_api_key
from .context import AppContext, get_context
from .errors import ApiError, StoreError
from .links import (
    SLUG_SHAPE,
    country_from_headers,
    generate_slug,
    referrer_host,
    validate_custom_slug,
    validate_target,
)
from .models import ApiKey, Link
from .stats import build_stats, parse_range

MAX_SLUG_ATTEMPTS = 5


class CreateLinkRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    target: str
    slug: str | None = None


def _not_found() -> ApiError:
    return ApiError(404, "not_found", "Link not found.")


async def _owned_link(slug: str, key: ApiKey, ctx: AppContext) -> Link:
    """Fetch a link the key's owner owns. Others' links look the same as missing ones."""
    link = await ctx.store.get_link(slug) if SLUG_SHAPE.match(slug) else None
    if link is None or link.owner_id != key.owner_id:
        raise _not_found()
    return link


async def _record_click(ctx: AppContext, slug: str, day: date, country: str, host: str) -> None:
    try:
        await ctx.store.record_click(slug, day, country, host)
    except Exception as exc:  # background work must never raise into the runtime
        print(f"record_click failed for slug={slug}: {exc!r}")


def create_app() -> FastAPI:
    # Interactive docs are off: they add routes and import cost on a 10 ms CPU budget.
    app = FastAPI(title="go.jbirdkerr.net", docs_url=None, redoc_url=None, openapi_url=None)

    @app.exception_handler(ApiError)
    async def _api_error(_: Request, exc: ApiError) -> JSONResponse:
        return JSONResponse(
            {"error": {"code": exc.code, "message": exc.message}},
            status_code=exc.status,
            headers=exc.headers,
        )

    @app.exception_handler(StoreError)
    async def _store_error(_: Request, exc: StoreError) -> JSONResponse:
        print(f"store error: {exc}")
        return JSONResponse(
            {"error": {"code": "storage_unavailable", "message": "Storage is temporarily unavailable."}},
            status_code=503,
        )

    @app.get("/health")
    async def health() -> dict[str, str]:
        return {"status": "ok"}  # deliberately no database call

    @app.post("/api/links", status_code=201)
    async def create_link(
        body: CreateLinkRequest,
        key: ApiKey = Depends(require_api_key),
        ctx: AppContext = Depends(get_context),
    ) -> dict[str, Any]:
        limit = await ctx.store.check_rate_limit(
            key.key_hash, ctx.settings.create_rate_limit, ctx.settings.create_rate_window_seconds
        )
        if not limit.allowed:
            raise ApiError(
                429,
                "rate_limited",
                "Too many links created. Try again later.",
                headers={"Retry-After": str(max(1, limit.retry_after))},
            )

        target = validate_target(body.target, ctx.settings, ctx.blocklist)

        if body.slug is not None:
            slug = validate_custom_slug(body.slug)
            link = await ctx.store.create_link(slug, target, key.owner_id)
            if link is None:
                raise ApiError(409, "slug_taken", "That slug is already in use.")
        else:
            link = None
            for _ in range(MAX_SLUG_ATTEMPTS):
                link = await ctx.store.create_link(generate_slug(), target, key.owner_id)
                if link is not None:
                    break
            if link is None:
                raise ApiError(503, "slug_generation_failed", "Could not generate a free slug. Try again.")

        return {
            "slug": link.slug,
            "short_url": f"{ctx.settings.base_url}/{link.slug}",
            "target": link.target,
            "created_at": link.created_at.isoformat(),
        }

    @app.get("/api/links/{slug}")
    async def get_link(
        slug: str,
        key: ApiKey = Depends(require_api_key),
        ctx: AppContext = Depends(get_context),
    ) -> dict[str, Any]:
        link = await _owned_link(slug, key, ctx)
        return {
            "slug": link.slug,
            "target": link.target,
            "created_at": link.created_at.isoformat(),
            "disabled": link.disabled,
        }

    @app.get("/api/links/{slug}/stats")
    async def link_stats(
        slug: str,
        range: str = Query("7d"),  # noqa: A002 - public query parameter name
        key: ApiKey = Depends(require_api_key),
        ctx: AppContext = Depends(get_context),
    ) -> dict[str, Any]:
        link = await _owned_link(slug, key, ctx)
        rng = parse_range(range, ctx.settings.max_range_days)
        today = ctx.now().date()
        start, end = rng.bounds(today)
        raw = await ctx.store.link_stats(slug, start, end, ctx.settings.top_n)
        return build_stats(link, rng, raw, today)

    @app.delete("/api/links/{slug}")
    async def disable_link(
        slug: str,
        key: ApiKey = Depends(require_api_key),
        ctx: AppContext = Depends(get_context),
    ) -> dict[str, Any]:
        link = await _owned_link(slug, key, ctx)
        await ctx.store.disable_link(link.slug)  # soft delete: the slug is never reused
        return {"slug": link.slug, "disabled": True}

    @app.api_route("/{slug}", methods=["GET", "HEAD"])
    async def redirect(slug: str, request: Request, ctx: AppContext = Depends(get_context)):
        if not SLUG_SHAPE.match(slug):
            raise _not_found()
        link = await ctx.store.get_link(slug)
        if link is None or link.disabled:
            raise _not_found()
        if request.method == "GET":
            # Computed now, written after the response: the redirect never waits on the write.
            ctx.schedule(
                _record_click(
                    ctx,
                    slug,
                    ctx.now().date(),
                    country_from_headers(request.headers),
                    referrer_host(request.headers.get("referer")),
                )
            )
        return RedirectResponse(link.target, status_code=302, headers={"Cache-Control": "no-store"})

    return app


app = create_app()
