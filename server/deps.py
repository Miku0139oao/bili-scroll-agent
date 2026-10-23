from __future__ import annotations

from collections.abc import AsyncIterator

from fastapi import Depends, HTTPException, Request
from sqlalchemy.ext.asyncio import AsyncSession

from .runtime import WebIdentity, database, resolve_session, verify_csrf


async def get_session(request: Request) -> AsyncIterator[AsyncSession]:
    async with database(request).session() as session:
        yield session


async def web_identity(
    request: Request,
    session: AsyncSession = Depends(get_session),
) -> WebIdentity:
    identity = await resolve_session(request, session)
    if not identity:
        raise HTTPException(status_code=401, detail="authentication required")
    return identity


async def require_csrf(
    request: Request,
    identity: WebIdentity = Depends(web_identity),
) -> WebIdentity:
    if request.method not in {"GET", "HEAD", "OPTIONS"} and not verify_csrf(request, identity):
        raise HTTPException(status_code=403, detail="invalid CSRF token")
    return identity


async def admin_identity(
    identity: WebIdentity = Depends(require_csrf),
) -> WebIdentity:
    if identity.user.role != "admin":
        raise HTTPException(status_code=403, detail="admin role required")
    return identity


def client_ip(request: Request) -> str:
    for key in ("cf-connecting-ip", "x-real-ip", "x-forwarded-for"):
        value = request.headers.get(key)
        if value:
            return value.split(",", 1)[0].strip()
    return request.client.host if request.client else "unknown"
