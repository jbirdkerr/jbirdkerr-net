from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any
from urllib.parse import urlsplit

DEFAULT_BASE_URL = "https://go.jbirdkerr.net"


class ConfigError(RuntimeError):
    pass


def _get(env: Any, name: str, default: Any = None) -> Any:
    """Read a var from a dict-like or attribute-style env (the Workers env is the latter)."""
    if env is None:
        return default
    value = env.get(name, default) if isinstance(env, Mapping) else getattr(env, name, default)
    return default if value is None else value


def _csv(value: Any) -> frozenset[str]:
    return frozenset(
        part.strip().lower().rstrip(".") for part in str(value or "").split(",") if part.strip()
    )


@dataclass(frozen=True)
class Settings:
    supabase_url: str
    supabase_service_key: str
    base_url: str = DEFAULT_BASE_URL
    blocked_hosts: frozenset[str] = frozenset()
    extra_self_hosts: frozenset[str] = frozenset()
    create_rate_limit: int = 30
    create_rate_window_seconds: int = 3600
    max_range_days: int = 90
    top_n: int = 10

    @property
    def self_hosts(self) -> frozenset[str]:
        """Hosts a target may never point at (redirect-loop protection)."""
        host = (urlsplit(self.base_url).hostname or "").lower()
        return frozenset({host, *self.extra_self_hosts}) - {""}

    @classmethod
    def from_env(cls, env: Any) -> Settings:
        url = str(_get(env, "SUPABASE_URL", "")).rstrip("/")
        key = str(_get(env, "SUPABASE_SERVICE_KEY", ""))
        if not url or not key:
            raise ConfigError("SUPABASE_URL and SUPABASE_SERVICE_KEY must be set")
        return cls(
            supabase_url=url,
            supabase_service_key=key,
            base_url=str(_get(env, "BASE_URL", DEFAULT_BASE_URL)).rstrip("/"),
            blocked_hosts=_csv(_get(env, "BLOCKED_HOSTS", "")),
            extra_self_hosts=_csv(_get(env, "EXTRA_SELF_HOSTS", "")),
            create_rate_limit=int(_get(env, "CREATE_RATE_LIMIT", 30)),
            create_rate_window_seconds=int(_get(env, "CREATE_RATE_WINDOW_SECONDS", 3600)),
            max_range_days=int(_get(env, "MAX_RANGE_DAYS", 90)),
            top_n=int(_get(env, "TOP_N", 10)),
        )
