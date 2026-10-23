from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..deps import get_session
from ..models import BalanceSnapshot, UsageRecord
from ..runtime import resolve_bearer
from ..schemas import BalanceImportRequest, UsageImportRequest

router = APIRouter(prefix="/ingest", tags=["ingest"])


async def _identity(request: Request, session: AsyncSession):
    owner = await resolve_bearer(request, session, request.headers.get("authorization"))
    if not owner or "usage:import" not in (owner.token.scopes or []):
        raise HTTPException(status_code=401, detail="usage import token required")
    return owner


@router.post("/usage")
async def ingest_usage(
    body: UsageImportRequest,
    request: Request,
    session: AsyncSession = Depends(get_session),
) -> dict:
    owner = await _identity(request, session)
    updated = 0
    for source_row in body.rows:
        row = await session.scalar(
            select(UsageRecord).where(
                UsageRecord.user_id == owner.user.id,
                UsageRecord.source == body.source,
                UsageRecord.date == source_row.date,
                UsageRecord.app_type == source_row.app_type,
                UsageRecord.provider_id == source_row.provider_id,
                UsageRecord.model == source_row.model,
            )
        )
        if not row:
            row = UsageRecord(
                user_id=owner.user.id,
                source=body.source,
                date=source_row.date,
                app_type=source_row.app_type,
                provider_id=source_row.provider_id,
                model=source_row.model,
            )
            session.add(row)
        row.request_count = source_row.request_count
        row.success_count = source_row.success_count
        row.input_tokens = source_row.input_tokens
        row.output_tokens = source_row.output_tokens
        row.cache_read_tokens = source_row.cache_read_tokens
        row.cache_creation_tokens = source_row.cache_creation_tokens
        row.total_cost_usd = source_row.total_cost_usd
        row.avg_latency_ms = source_row.avg_latency_ms
        updated += 1
    return {"ok": True, "updated": updated}


@router.post("/balance")
async def ingest_balance(
    body: BalanceImportRequest,
    request: Request,
    session: AsyncSession = Depends(get_session),
) -> dict:
    owner = await _identity(request, session)
    fetched_at = _parse_time(body.fetched_at)
    inserted = 0
    for item in body.balances:
        existing = await session.scalar(
            select(BalanceSnapshot.id).where(
                BalanceSnapshot.user_id == owner.user.id,
                BalanceSnapshot.provider_id == body.provider_id,
                BalanceSnapshot.currency == item.currency,
                BalanceSnapshot.fetched_at == fetched_at,
            )
        )
        if existing:
            continue
        session.add(
            BalanceSnapshot(
                user_id=owner.user.id,
                provider_id=body.provider_id,
                currency=item.currency,
                total_balance=item.total,
                granted_balance=item.granted,
                topped_up_balance=item.topped_up,
                is_available=body.available,
                low_balance=item.low or item.total < body.low_threshold,
                source=body.source,
                fetched_at=fetched_at,
            )
        )
        inserted += 1
    return {"ok": True, "inserted": inserted}


def _parse_time(value: str | None) -> datetime:
    if not value:
        return datetime.now(UTC)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return parsed.astimezone(UTC)
    except ValueError as exc:
        raise HTTPException(status_code=422, detail="invalid fetched_at") from exc
