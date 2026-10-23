from __future__ import annotations

import sqlite3
from datetime import UTC, datetime
from pathlib import Path

from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy import desc, func, select
from sqlalchemy import update as sql_update
from sqlalchemy.ext.asyncio import AsyncSession

from ..audit import record_audit
from ..deps import admin_identity, get_session
from ..models import (
    AuditEvent,
    AuthSession,
    BiliCredential,
    Invite,
    ProviderAccount,
    Report,
    Run,
    Schedule,
    User,
    now_utc,
)
from ..schemas import (
    AdminRunnerSettings,
    AdminUserPatch,
    InviteCreateRequest,
    ProviderAccountUpdate,
)
from ..security import new_totp_secret, totp_uri
from ..services.auth import AuthError, count_admins, create_invite
from ..services.balance import BalanceError, provider_view, refresh_deepseek_balance
from ..services.config import (
    load_admin_settings,
    load_config,
    save_admin_settings,
)
from ..services.runners import runner_status

router = APIRouter(prefix="/admin", tags=["admin"])


@router.get("/users")
async def users(
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    rows = (await session.scalars(select(User).order_by(User.created_at))).all()
    result = []
    for user in rows:
        config = await load_config(session, user.id)
        schedule = await session.get(Schedule, user.id)
        credential = await session.get(BiliCredential, user.id)
        result.append(
            {
                "id": user.id,
                "username": user.username,
                "display_name": user.display_name,
                "role": user.role,
                "disabled": user.disabled,
                "totp_confirmed": user.totp_confirmed,
                "created_at": user.created_at,
                "last_login_at": user.last_login_at,
                "runner": config.get("runner"),
                "bilibili_logged_in": bool(credential),
                "schedule": {
                    "enabled": bool(schedule and schedule.enabled),
                    "time": schedule.local_time if schedule else "20:00",
                    "timezone": schedule.timezone if schedule else "Asia/Shanghai",
                },
            }
        )
    return {"ok": True, "users": result}


@router.post("/invites")
async def invite_create(
    body: InviteCreateRequest,
    request: Request,
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    try:
        invite, token = await create_invite(
            session,
            request.app.state.cipher,
            request.app.state.settings,
            created_by=identity.user.id,
            username=body.username,
            display_name=body.display_name,
            role=body.role,
        )
    except AuthError as exc:
        raise HTTPException(status_code=409, detail=str(exc)) from exc
    await record_audit(
        session,
        event="admin.invite_create",
        user_id=invite.id,
        actor=identity.user,
        target=invite.username,
    )
    return {
        "ok": True,
        "id": invite.id,
        "username": invite.username,
        "expires_at": invite.expires_at,
        "invite_url": f"{request.app.state.settings.public_url.rstrip('/')}/invite/{token}",
    }


@router.get("/invites")
async def invite_list(
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    rows = (await session.scalars(select(Invite).order_by(desc(Invite.created_at)).limit(100))).all()
    return {
        "ok": True,
        "invites": [
            {
                "id": item.id,
                "username": item.username,
                "display_name": item.display_name,
                "role": item.role,
                "expires_at": item.expires_at,
                "used_at": item.used_at,
            }
            for item in rows
        ],
    }


@router.patch("/users/{user_id}")
async def user_patch(
    user_id: str,
    body: AdminUserPatch,
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    user = await session.get(User, user_id)
    if not user:
        raise HTTPException(status_code=404, detail="user not found")
    if user.id == identity.user.id and (body.disabled or body.role == "member"):
        raise HTTPException(status_code=409, detail="cannot disable or demote yourself")
    if user.role == "admin" and (body.disabled or body.role == "member"):
        if await count_admins(session) <= 1:
            raise HTTPException(status_code=409, detail="cannot remove the last admin")
    if body.disabled is not None:
        user.disabled = body.disabled
        if body.disabled:
            await session.execute(
                sql_update(AuthSession)
                .where(AuthSession.user_id == user.id)
                .values(revoked_at=now_utc())
            )
    if body.role is not None:
        user.role = body.role
    await record_audit(
        session,
        event="admin.user_update",
        user_id=user.id,
        actor=identity.user,
        detail=body.model_dump(exclude_none=True),
    )
    return {
        "ok": True,
        "user": {
            "id": user.id,
            "username": user.username,
            "role": user.role,
            "disabled": user.disabled,
        },
    }


@router.post("/users/{user_id}/reset-totp")
async def reset_totp(
    user_id: str,
    request: Request,
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    user = await session.get(User, user_id)
    if not user:
        raise HTTPException(status_code=404, detail="user not found")
    secret = new_totp_secret()
    user.totp_secret_enc = request.app.state.cipher.encrypt_bytes(
        secret.encode("ascii"), aad=f"totp:{user.username}".encode()
    )
    user.totp_confirmed = False
    await session.execute(
        sql_update(AuthSession)
        .where(AuthSession.user_id == user.id)
        .values(revoked_at=now_utc())
    )
    await record_audit(
        session,
        event="admin.totp_reset",
        user_id=user.id,
        actor=identity.user,
    )
    return {
        "ok": True,
        "username": user.username,
        "secret": secret,
        "otpauth_uri": totp_uri(secret, user.username),
    }


@router.get("/settings")
async def get_settings(
    request: Request,
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    return {
        "ok": True,
        "settings": await load_admin_settings(session),
        "runners": runner_status(request.app.state.settings),
    }


@router.put("/settings")
async def put_settings(
    body: AdminRunnerSettings,
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    values = await save_admin_settings(session, body.model_dump(mode="json", exclude_none=True))
    await record_audit(
        session,
        event="admin.settings_update",
        actor=identity.user,
        detail=values,
    )
    return {"ok": True, "settings": values}


@router.get("/audit")
async def audit(
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    rows = (await session.scalars(select(AuditEvent).order_by(desc(AuditEvent.created_at)).limit(500))).all()
    return {
        "ok": True,
        "events": [
            {
                "id": event.id,
                "event": event.event,
                "user_id": event.user_id,
                "actor_user_id": event.actor_user_id,
                "target": event.target,
                "detail": event.detail,
                "ip": event.ip,
                "created_at": event.created_at,
            }
            for event in rows
        ],
    }


@router.get("/health")
async def health(
    request: Request,
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    db_path = Path(request.app.state.settings.data_dir) / "bili-scroll.db"
    return {
        "ok": True,
        "version": request.app.state.version,
        "database": {
            "path": str(db_path),
            "size_bytes": db_path.stat().st_size if db_path.exists() else 0,
        },
        "scheduler": {
            "running": bool(request.app.state.scheduler._task)
            and not request.app.state.scheduler._task.done(),
        },
        "runners": runner_status(request.app.state.settings),
        "counts": {
            "users": int(await session.scalar(select(func.count()).select_from(User)) or 0),
            "runs": int(await session.scalar(select(func.count()).select_from(Run)) or 0),
            "reports": int(await session.scalar(select(func.count()).select_from(Report)) or 0),
        },
    }


@router.post("/backup")
async def backup(
    request: Request,
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    source = Path(request.app.state.settings.data_dir) / "bili-scroll.db"
    target_dir = Path(request.app.state.settings.data_dir) / "backups"
    target_dir.mkdir(parents=True, exist_ok=True)
    target = target_dir / f"bili-scroll-{datetime.now(UTC).strftime('%Y%m%d-%H%M%S')}.db"
    if source.exists():
        with sqlite3.connect(source) as src, sqlite3.connect(target) as dst:
            src.backup(dst)
    await record_audit(
        session,
        event="admin.backup",
        actor=identity.user,
        target=str(target),
    )
    return {"ok": True, "path": str(target)}


@router.get("/providers")
async def providers(
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    account = await session.get(ProviderAccount, "deepseek")
    return {"ok": True, "deepseek": provider_view(account)}


@router.put("/providers/deepseek")
async def update_deepseek_provider(
    body: ProviderAccountUpdate,
    request: Request,
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    account = await session.get(ProviderAccount, "deepseek")
    if not account:
        account = ProviderAccount(provider_id="deepseek")
        session.add(account)
    account.base_url = body.base_url.rstrip("/")
    account.low_balance_threshold = body.low_balance_threshold
    account.enabled = body.enabled
    if body.api_key:
        account.api_key_enc = request.app.state.cipher.encrypt_bytes(
            body.api_key.strip().encode("utf-8"), aad=b"provider:deepseek"
        )
    account.updated_at = now_utc()
    await record_audit(
        session,
        event="admin.provider_update",
        actor=identity.user,
        target="deepseek",
        detail={
            "base_url": account.base_url,
            "has_api_key": bool(account.api_key_enc),
            "low_balance_threshold": account.low_balance_threshold,
        },
    )
    return {"ok": True, "deepseek": provider_view(account)}


@router.post("/providers/deepseek/check")
async def check_deepseek_balance(
    request: Request,
    identity=Depends(admin_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    try:
        result = await refresh_deepseek_balance(session, request.app.state.cipher, user_id=identity.user.id)
        return {"ok": True, **result}
    except BalanceError as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc
