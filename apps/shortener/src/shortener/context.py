from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any

from fastapi import Request

from .config import Settings
from .db import FetchTransport, Store, SupabaseStore
from .links import Blocklist, ConfigBlocklist


def schedule_in_background(coro: Awaitable[None]) -> None:
    """Run a coroutine after the response is sent, keeping the Worker alive until it finishes."""
    try:
        from workers import wait_until  # pants: no-infer-dep
    except ImportError:  # local runs outside the Workers runtime
        asyncio.ensure_future(coro)
        return
    wait_until(coro)


@dataclass
class AppContext:
    settings: Settings
    store: Store
    blocklist: Blocklist
    schedule: Callable[[Awaitable[None]], None]
    now: Callable[[], datetime]


def build_context(env: Any) -> AppContext:
    settings = Settings.from_env(env)
    return AppContext(
        settings=settings,
        store=SupabaseStore(settings, FetchTransport()),
        blocklist=ConfigBlocklist(settings.blocked_hosts),
        schedule=schedule_in_background,
        now=lambda: datetime.now(timezone.utc),
    )


def get_context(request: Request) -> AppContext:
    """Built once per isolate from the Worker env; tests set app.state.ctx directly."""
    ctx = getattr(request.app.state, "ctx", None)
    if ctx is None:
        ctx = build_context(request.scope.get("env"))
        request.app.state.ctx = ctx
    return ctx
