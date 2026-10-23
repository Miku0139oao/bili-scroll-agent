from __future__ import annotations

from copy import deepcopy
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from sqlalchemy.ext.asyncio import AsyncSession

from ..models import Schedule, SystemSetting, UserConfig
from ..schemas import MemberConfig, MemberConfigPatch, ScheduleConfig
from ..timeutil import next_daily_at

DEFAULTS = MemberConfig().model_dump(mode="json")

ADMIN_DEFAULTS = {
    "default_runner": "codex",
    "codex_provider": "",
    "codex_model": "",
    "codex_reasoning": "",
    "hermes_provider": "",
    "hermes_model": "",
    "outbound_enabled": True,
    "global_concurrency": 1,
    "manual_runs_per_day": 3,
    "scheduled_runs_per_day": 1,
    "retention_days": 365,
}


async def load_config(session: AsyncSession, user_id: str) -> dict:
    row = await session.get(UserConfig, user_id)
    payload = deepcopy(DEFAULTS)
    if row and isinstance(row.payload, dict):
        payload.update(row.payload)
    payload["schedule"] = {**DEFAULTS["schedule"], **(payload.get("schedule") or {})}
    return payload


async def save_config(
    session: AsyncSession,
    user_id: str,
    patch: MemberConfigPatch,
) -> dict:
    current = await load_config(session, user_id)
    changes = patch.model_dump(mode="json", exclude_none=True)
    if "schedule" in changes and isinstance(changes["schedule"], dict):
        current["schedule"] = {**current["schedule"], **changes.pop("schedule")}
    current.update(changes)
    validated = MemberConfig.model_validate(current)
    payload = validated.model_dump(mode="json")

    row = await session.get(UserConfig, user_id)
    if row:
        row.payload = payload
    else:
        session.add(UserConfig(user_id=user_id, payload=payload))

    await _sync_schedule(session, user_id, validated.schedule)
    return payload


async def _sync_schedule(session: AsyncSession, user_id: str, config: ScheduleConfig) -> None:
    schedule = await session.get(Schedule, user_id)
    if not schedule:
        schedule = Schedule(user_id=user_id)
        session.add(schedule)
    must_reschedule = (
        not schedule.enabled
        or schedule.local_time != config.time
        or schedule.timezone != config.timezone
        or schedule.next_run_at is None
    )
    schedule.enabled = config.enabled
    schedule.local_time = config.time
    schedule.timezone = config.timezone
    if config.enabled and must_reschedule:
        schedule.next_run_at = next_daily_at(config.time, config.timezone)
    elif not config.enabled:
        schedule.next_run_at = None


async def load_admin_settings(session: AsyncSession) -> dict:
    row = await session.get(SystemSetting, "admin")
    result = deepcopy(ADMIN_DEFAULTS)
    if row and isinstance(row.value, dict):
        result.update(row.value)
    return result


async def save_admin_settings(session: AsyncSession, patch: dict) -> dict:
    current = await load_admin_settings(session)
    current.update({k: v for k, v in patch.items() if v is not None})
    row = await session.get(SystemSetting, "admin")
    if row:
        row.value = current
    else:
        session.add(SystemSetting(key="admin", value=current))
    return current


def validate_timezone(name: str) -> str:
    try:
        ZoneInfo(name)
    except ZoneInfoNotFoundError as exc:
        raise ValueError(f"unknown timezone: {name}") from exc
    return name
