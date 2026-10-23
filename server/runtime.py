from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime

from fastapi import Request
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from .config import Settings
from .crypto import SecretCipher
from .db import Database
from .models import ApiToken, AuthSession, User
from .security import utcnow


@dataclass(slots=True)
class WebIdentity:
    user: User
    session: AuthSession


@dataclass(slots=True)
class BearerIdentity:
    user: User
    token: ApiToken
    kind: str


def settings(request: Request) -> Settings:
    return request.app.state.settings


def cipher(request: Request) -> SecretCipher:
    return request.app.state.cipher


def database(request: Request) -> Database:
    return request.app.state.db


async def resolve_session(
    request: Request,
    session: AsyncSession,
    cookie_name: str = "bili_session",
) -> WebIdentity | None:
    raw = request.cookies.get(cookie_name)
    if not raw:
        return None
    token_hash = cipher(request).token_hash(raw)
    row = await session.scalar(
        select(AuthSession).where(
            AuthSession.id_hash == token_hash,
            AuthSession.revoked_at.is_(None),
        )
    )
    if not row:
        return None
    now = utcnow()
    if _as_utc(row.expires_at) <= now or _as_utc(row.idle_expires_at) <= now:
        row.revoked_at = now
        return None
    user = await session.get(User, row.user_id)
    if not user or user.disabled or not user.totp_confirmed:
        return None
    row.idle_expires_at = now.replace(microsecond=0)
    from datetime import timedelta

    row.idle_expires_at = now + timedelta(minutes=settings(request).session_idle_minutes)
    return WebIdentity(user=user, session=row)


async def resolve_bearer(
    request: Request,
    session: AsyncSession,
    authorization: str | None,
    secret_cipher: SecretCipher | None = None,
) -> BearerIdentity | None:
    if not authorization or not authorization.lower().startswith("bearer "):
        return None
    raw = authorization.split(" ", 1)[1].strip()
    if not raw:
        return None
    active_cipher = secret_cipher or cipher(request)
    row = await session.scalar(
        select(ApiToken).where(
            ApiToken.id_hash == active_cipher.token_hash(raw),
            ApiToken.revoked_at.is_(None),
        )
    )
    if not row:
        return None
    now = utcnow()
    if row.expires_at and _as_utc(row.expires_at) <= now:
        return None
    user = await session.get(User, row.user_id)
    if not user or user.disabled or not user.totp_confirmed:
        return None
    row.last_used_at = now
    return BearerIdentity(user=user, token=row, kind=row.kind)


def verify_csrf(request: Request, identity: WebIdentity) -> bool:
    candidate = request.headers.get("x-csrf-token", "")
    if not candidate:
        return False
    return cipher(request).token_hash(candidate) == identity.session.csrf_hash


def _as_utc(value: datetime) -> datetime:
    from datetime import UTC

    return value if value.tzinfo else value.replace(tzinfo=UTC)
