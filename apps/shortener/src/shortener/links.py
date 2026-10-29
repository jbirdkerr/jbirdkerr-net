"""Pure validation and parsing helpers for links and click metadata."""
from __future__ import annotations

import re
import secrets
from collections.abc import Mapping
from typing import Protocol
from urllib.parse import urlsplit

from .config import Settings
from .errors import ApiError

SLUG_ALPHABET = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
GENERATED_SLUG_LENGTH = 7

# Anything that does not look like this cannot be a slug, so the redirect path
# can 404 without spending a database subrequest (favicon.ico, scanners, ...).
SLUG_SHAPE = re.compile(r"^[A-Za-z0-9-]{3,32}$")
CUSTOM_SLUG = re.compile(r"^[A-Za-z0-9][A-Za-z0-9-]{1,30}[A-Za-z0-9]$")

# Includes the Phase 2 web routes so they can never be shadowed by a slug.
RESERVED_SLUGS = frozenset(
    {"api", "health", "static", "login", "logout", "auth", "links",
     "docs", "admin", "favicon", "robots", "index"}
)

MAX_TARGET_LENGTH = 2048
_FORBIDDEN_IN_TARGET = re.compile(r"[\s\\\x00-\x1f\x7f]")


class Blocklist(Protocol):
    """Swap this out for a provider (e.g. Safe Browsing) later; the rest of the app only calls this."""

    def is_blocked(self, host: str) -> bool: ...


class ConfigBlocklist:
    """Blocks exact hosts and their subdomains, from a configured list."""

    def __init__(self, hosts: frozenset[str]) -> None:
        self._hosts = hosts

    def is_blocked(self, host: str) -> bool:
        return any(host == h or host.endswith("." + h) for h in self._hosts)


def _invalid_target(code: str, message: str) -> ApiError:
    return ApiError(422, code, message)


def validate_target(raw: str, settings: Settings, blocklist: Blocklist) -> str:
    target = raw.strip()
    if not target:
        raise _invalid_target("invalid_target", "Target URL is required.")
    if len(target) > MAX_TARGET_LENGTH:
        raise _invalid_target("invalid_target", f"Target URL must be at most {MAX_TARGET_LENGTH} characters.")
    if _FORBIDDEN_IN_TARGET.search(target):
        raise _invalid_target("invalid_target", "Target URL must not contain whitespace, backslashes, or control characters.")
    try:
        parts = urlsplit(target)
        host = parts.hostname
        _ = parts.port  # raises ValueError on a malformed port
    except ValueError:
        raise _invalid_target("invalid_target", "Target is not a valid URL.") from None
    if parts.scheme.lower() not in {"http", "https"}:
        raise _invalid_target("unsupported_scheme", "Target must be an absolute http or https URL.")
    if not host:
        raise _invalid_target("invalid_target", "Target URL must include a host.")
    if parts.username is not None or parts.password is not None:
        raise _invalid_target("invalid_target", "Target URL must not include credentials.")
    host = host.rstrip(".").lower()
    if host in settings.self_hosts:
        raise _invalid_target("redirect_loop", "Target must not point back to this service.")
    if blocklist.is_blocked(host):
        raise _invalid_target("blocked_target", "Target host is not allowed.")
    return target


def validate_custom_slug(slug: str) -> str:
    if not CUSTOM_SLUG.match(slug):
        raise ApiError(
            422,
            "invalid_slug",
            "Slug must be 3-32 characters: letters, digits, and hyphens, "
            "starting and ending with a letter or digit.",
        )
    if slug.lower() in RESERVED_SLUGS:
        raise ApiError(422, "reserved_slug", "That slug is reserved.")
    return slug


def generate_slug() -> str:
    while True:
        slug = "".join(secrets.choice(SLUG_ALPHABET) for _ in range(GENERATED_SLUG_LENGTH))
        if slug.lower() not in RESERVED_SLUGS:
            return slug


def referrer_host(value: str | None) -> str:
    """Hostname of the Referer header only, or 'direct'. Never the path or query."""
    if not value:
        return "direct"
    try:
        host = urlsplit(value.strip()).hostname
    except ValueError:
        return "direct"
    host = (host or "").rstrip(".").lower()[:253]
    return host or "direct"


_COUNTRY = re.compile(r"^[A-Z]{2}$")
_NOT_A_COUNTRY = {"XX", "T1"}  # Cloudflare: unknown / Tor


def country_from_headers(headers: Mapping[str, str]) -> str:
    """Coarse country from Cloudflare's CF-IPCountry header, else 'unknown'."""
    value = (headers.get("cf-ipcountry") or "").strip().upper()
    return value if _COUNTRY.match(value) and value not in _NOT_A_COUNTRY else "unknown"
