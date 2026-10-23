from __future__ import annotations

from collections import defaultdict
from datetime import UTC, datetime, timedelta

from sqlalchemy import desc, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..models import BalanceSnapshot, Run, UsageRecord, now_utc


async def usage_dashboard(session: AsyncSession, user_id: str, days: int = 30) -> dict:
    days = max(1, min(days, 365))
    cutoff = (datetime.now(UTC) - timedelta(days=days - 1)).date().isoformat()
    usage = (
        await session.scalars(
            select(UsageRecord).where(
                UsageRecord.user_id == user_id,
                UsageRecord.date >= cutoff,
            )
        )
    ).all()
    daily: dict[str, dict] = defaultdict(
        lambda: {
            "date": "",
            "requests": 0,
            "input_tokens": 0,
            "output_tokens": 0,
            "cache_read_tokens": 0,
            "cost_usd": 0.0,
        }
    )
    providers: dict[tuple[str, str, str], dict] = {}
    for row in usage:
        day = daily[row.date]
        day["date"] = row.date
        day["requests"] += row.request_count
        day["input_tokens"] += row.input_tokens
        day["output_tokens"] += row.output_tokens
        day["cache_read_tokens"] += row.cache_read_tokens
        day["cost_usd"] += row.total_cost_usd
        key = (row.app_type, row.provider_id, row.model)
        item = providers.setdefault(
            key,
            {
                "app_type": row.app_type,
                "provider": row.provider_id,
                "model": row.model,
                "requests": 0,
                "input_tokens": 0,
                "output_tokens": 0,
                "cache_read_tokens": 0,
                "cost_usd": 0.0,
            },
        )
        item["requests"] += row.request_count
        item["input_tokens"] += row.input_tokens
        item["output_tokens"] += row.output_tokens
        item["cache_read_tokens"] += row.cache_read_tokens
        item["cost_usd"] += row.total_cost_usd

    runs = (
        await session.scalars(
            select(Run)
            .where(
                Run.user_id == user_id,
                Run.status.in_(["succeeded", "failed", "cancelled"]),
            )
            .order_by(desc(Run.finished_at))
            .limit(50)
        )
    ).all()
    run_rows = []
    for run in runs:
        telemetry = run.telemetry or {}
        usage_data = telemetry.get("usage") or {}
        run_rows.append(
            {
                "id": run.id,
                "runner": run.runner,
                "provider": telemetry.get("provider") or "",
                "model": telemetry.get("model") or "",
                "input_tokens": int(usage_data.get("input_tokens") or 0),
                "output_tokens": int(usage_data.get("output_tokens") or 0),
                "cache_read_tokens": int(usage_data.get("cache_read_tokens") or 0),
                "cost_usd": float(usage_data.get("cost_usd") or 0),
                "status": run.status,
                "finished_at": run.finished_at,
            }
        )

    balances = (
        await session.scalars(
            select(BalanceSnapshot)
            .where(BalanceSnapshot.user_id == user_id)
            .order_by(desc(BalanceSnapshot.fetched_at))
            .limit(20)
        )
    ).all()
    latest_balances = []
    seen = set()
    for row in balances:
        key = (row.provider_id, row.currency)
        if key in seen:
            continue
        seen.add(key)
        latest_balances.append(
            {
                "provider": row.provider_id,
                "currency": row.currency,
                "total": row.total_balance,
                "granted": row.granted_balance,
                "topped_up": row.topped_up_balance,
                "available": row.is_available,
                "low": row.low_balance,
                "source": row.source,
                "fetched_at": row.fetched_at,
            }
        )

    totals = {
        "requests": sum(row.request_count for row in usage),
        "input_tokens": sum(row.input_tokens for row in usage),
        "output_tokens": sum(row.output_tokens for row in usage),
        "cache_read_tokens": sum(row.cache_read_tokens for row in usage),
        "cost_usd": round(sum(row.total_cost_usd for row in usage), 6),
    }
    input_total = totals["input_tokens"] + totals["cache_read_tokens"]
    totals["cache_hit_percent"] = (
        round(100 * totals["cache_read_tokens"] / input_total, 1) if input_total else 0
    )
    return {
        "days": days,
        "since": cutoff,
        "totals": totals,
        "daily": sorted(daily.values(), key=lambda row: row["date"]),
        "providers": sorted(providers.values(), key=lambda row: row["cost_usd"], reverse=True),
        "runs": run_rows,
        "balances": latest_balances,
        "generated_at": now_utc(),
        "sources": [
            "service runs",
            "cc-switch usage import",
            "DeepSeek balance dashboard import",
        ],
    }
