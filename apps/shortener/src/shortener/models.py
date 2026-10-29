from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime


@dataclass(frozen=True)
class ApiKey:
    key_hash: str
    label: str
    owner_id: str


@dataclass(frozen=True)
class Link:
    slug: str
    target: str
    owner_id: str
    created_at: datetime
    disabled: bool


@dataclass(frozen=True)
class RateLimitResult:
    allowed: bool
    retry_after: int
