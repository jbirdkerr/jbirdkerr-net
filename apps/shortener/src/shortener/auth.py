from __future__ import annotations

import hashlib

from fastapi import Depends, Request

from .context import AppContext, get_context
from .errors import ApiError
from .models import ApiKey


def hash_key(raw_key: str) -> str:
    """SHA-256 of the raw key. Keys are long random tokens, so a fast hash is appropriate."""
    return hashlib.sha256(raw_key.encode("utf-8")).hexdigest()


def _unauthorized() -> ApiError:
    return ApiError(
        401, "unauthorized", "A valid API key is required.", headers={"WWW-Authenticate": "Bearer"}
    )


async def require_api_key(request: Request, ctx: AppContext = Depends(get_context)) -> ApiKey:
    scheme, _, token = request.headers.get("authorization", "").partition(" ")
    token = token.strip()
    if scheme.lower() != "bearer" or not token:
        raise _unauthorized()
    key = await ctx.store.get_api_key(hash_key(token))
    if key is None:
        raise _unauthorized()
    return key
