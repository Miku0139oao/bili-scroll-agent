from __future__ import annotations

from datetime import UTC, datetime, timedelta
from zoneinfo import ZoneInfo


def as_utc(value: datetime | None) -> datetime | None:
    if value is None:
        return None
    return value if value.tzinfo else value.replace(tzinfo=UTC)


def next_daily_at(local_time: str, timezone: str, *, after: datetime | None = None) -> datetime:
    zone = ZoneInfo(timezone)
    current = (after or datetime.now(UTC)).astimezone(zone)
    hour, minute = (int(part) for part in local_time.split(":", 1))
    candidate = current.replace(hour=hour, minute=minute, second=0, microsecond=0)
    if candidate <= current:
        candidate += timedelta(days=1)
    return candidate.astimezone(UTC)
