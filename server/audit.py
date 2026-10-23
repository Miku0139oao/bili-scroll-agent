from __future__ import annotations

from sqlalchemy.ext.asyncio import AsyncSession

from .models import AuditEvent, User


async def record_audit(
    session: AsyncSession,
    *,
    event: str,
    user_id: str | None = None,
    actor: User | None = None,
    target: str | None = None,
    detail: dict | None = None,
    ip: str | None = None,
) -> None:
    session.add(
        AuditEvent(
            user_id=user_id,
            actor_user_id=actor.id if actor else None,
            event=event,
            target=target,
            detail=detail or {},
            ip=ip,
        )
    )
