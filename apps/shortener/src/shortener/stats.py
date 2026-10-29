from __future__ import annotations

import math
import re
from dataclasses import dataclass
from datetime import date, timedelta
from typing import Any

from .errors import ApiError
from .models import Link

_RANGE = re.compile(r"^(\d{1,3})([hd])$")


@dataclass(frozen=True)
class StatsRange:
    label: str
    days: int

    def bounds(self, today: date) -> tuple[date, date]:
        return today - timedelta(days=self.days - 1), today


def parse_range(value: str, max_days: int) -> StatsRange:
    """Ranges are whole UTC days ending today. Hours round up: 24h -> 1 day, 36h -> 2 days.

    Click counts are stored per day, so sub-day precision is not available.
    """
    match = _RANGE.match(value.strip().lower())
    if not match:
        raise ApiError(422, "invalid_range", "range must look like 24h, 7d, or 30d.")
    amount, unit = int(match.group(1)), match.group(2)
    days = amount if unit == "d" else math.ceil(amount / 24)
    if days < 1 or days > max_days:
        raise ApiError(422, "invalid_range", f"range must cover between 1 and {max_days} days.")
    return StatsRange(label=f"{amount}{unit}", days=days)


def build_stats(link: Link, rng: StatsRange, raw: dict[str, Any], today: date) -> dict[str, Any]:
    start, end = rng.bounds(today)
    by_day = {str(row["day"]): int(row["clicks"]) for row in raw.get("daily") or []}
    daily = []
    for offset in range(rng.days):
        day = (start + timedelta(days=offset)).isoformat()
        daily.append({"day": day, "clicks": by_day.get(day, 0)})
    return {
        "slug": link.slug,
        "target": link.target,
        "created_at": link.created_at.isoformat(),
        "disabled": link.disabled,
        "total_clicks": int(raw.get("total_clicks") or 0),
        "range": rng.label,
        "from": start.isoformat(),
        "to": end.isoformat(),
        "clicks_in_range": int(raw.get("clicks_in_range") or 0),
        "daily": daily,
        "top_referrers": [
            {"host": r["host"], "clicks": int(r["clicks"])} for r in raw.get("top_referrers") or []
        ],
        "top_countries": [
            {"country": r["country"], "clicks": int(r["clicks"])} for r in raw.get("top_countries") or []
        ],
    }
