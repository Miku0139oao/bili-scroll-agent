from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Request, Response
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..audit import record_audit
from ..config import Settings
from ..deps import client_ip, get_session, web_identity
from ..models import ApiToken, Invite, User
from ..schemas import (
    InviteAcceptRequest,
    InviteVerifyRequest,
    LoginRequest,
    StepUpRequest,
)
from ..security import utcnow, verify_password, verify_totp
from ..services.auth import (
    AuthError,
    accept_invite,
    authenticate_user,
    confirm_invite_totp,
    create_session,
    revoke_session,
)
from ..timeutil import as_utc

router = APIRouter(prefix="/auth", tags=["auth"])


def _public_user(user: User) -> dict:
    return {
        "id": user.id,
        "username": user.username,
        "display_name": user.display_name,
        "role": user.role,
    }


def _set_session_cookie(response: Response, settings: Settings, token: str) -> None:
    response.set_cookie(
        "bili_session",
        token,
        max_age=settings.session_hours * 3600,
        httponly=True,
        secure=settings.secure_cookies,
        samesite="strict",
        path="/",
    )


@router.post("/login")
async def login(
    body: LoginRequest,
    request: Request,
    response: Response,
    session: AsyncSession = Depends(get_session),
) -> dict:
    limiter = request.app.state.rate_limiter
    ip = client_ip(request)
    if not await limiter.allow(f"login:{ip}:{body.username.lower()}", 10):
        raise HTTPException(status_code=429, detail="too many login attempts")
    try:
        user = await authenticate_user(
            session,
            request.app.state.cipher,
            username=body.username,
            password=body.password,
            totp=body.totp,
        )
    except AuthError as exc:
        await record_audit(session, event="auth.login_failed", target=body.username, ip=ip)
        raise HTTPException(status_code=401, detail=str(exc)) from exc
    token, csrf = await create_session(
        ip,
        session,
        request.app.state.cipher,
        request.app.state.settings,
        user,
    )
    _set_session_cookie(response, request.app.state.settings, token)
    await record_audit(session, event="auth.login", user_id=user.id, actor=user, ip=ip)
    return {"ok": True, "user": _public_user(user), "csrf": csrf}


@router.get("/me")
async def me(
    request: Request,
    identity=Depends(web_identity),
) -> dict:
    session = identity.session
    # The raw CSRF token is never stored. A fresh token is issued at login only.
    # For reloads the frontend can obtain a replacement with POST /auth/csrf.
    return {"ok": True, "user": _public_user(identity.user), "session_expires": session.expires_at}


@router.post("/csrf")
async def rotate_csrf(
    request: Request,
    response: Response,
    identity=Depends(web_identity),
) -> dict:
    from ..crypto import new_secret

    raw = new_secret("bili_csrf", 24)
    identity.session.csrf_hash = request.app.state.cipher.token_hash(raw)
    return {"ok": True, "csrf": raw}


@router.post("/logout")
async def logout(
    response: Response,
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    await revoke_session(session, identity.session)
    response.delete_cookie("bili_session", path="/")
    return {"ok": True}


@router.get("/invite/{token}")
async def invite_info(
    token: str,
    request: Request,
    session: AsyncSession = Depends(get_session),
) -> dict:
    invite = await session.scalar(
        select(Invite).where(
            Invite.token_hash == request.app.state.cipher.token_hash(token),
            Invite.used_at.is_(None),
        )
    )
    if not invite or (invite.expires_at and as_utc(invite.expires_at) <= utcnow()):
        raise HTTPException(status_code=404, detail="invite is invalid or expired")
    return {
        "ok": True,
        "username": invite.username,
        "display_name": invite.display_name,
        "expires_at": invite.expires_at,
    }


@router.post("/invite/accept")
async def invite_accept(
    body: InviteAcceptRequest,
    request: Request,
    session: AsyncSession = Depends(get_session),
) -> dict:
    try:
        return {
            "ok": True,
            **await accept_invite(
                session,
                request.app.state.cipher,
                token=body.token,
                password=body.password,
            ),
        }
    except AuthError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc


@router.post("/invite/verify")
async def invite_verify(
    body: InviteVerifyRequest,
    request: Request,
    response: Response,
    session: AsyncSession = Depends(get_session),
) -> dict:
    try:
        user = await confirm_invite_totp(
            session,
            request.app.state.cipher,
            setup_token=body.setup_token,
            code=body.totp,
        )
    except AuthError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    token, csrf = await create_session(
        client_ip(request),
        session,
        request.app.state.cipher,
        request.app.state.settings,
        user,
    )
    _set_session_cookie(response, request.app.state.settings, token)
    return {"ok": True, "user": _public_user(user), "csrf": csrf}


@router.post("/step-up")
async def step_up(
    body: StepUpRequest,
    request: Request,
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    user = identity.user
    if not verify_password(user.password_hash, body.password):
        raise HTTPException(status_code=400, detail="invalid password or TOTP")
    assert user.totp_secret_enc
    secret = request.app.state.cipher.decrypt_bytes(
        user.totp_secret_enc, aad=f"totp:{user.username}".encode()
    ).decode("ascii")
    if not verify_totp(secret, body.totp):
        raise HTTPException(status_code=400, detail="invalid password or TOTP")
    from ..crypto import new_secret
    from ..security import expires

    raw = new_secret("bili_stepup", 32)
    session.add(
        ApiToken(
            id_hash=request.app.state.cipher.token_hash(raw),
            user_id=user.id,
            label="outbound-enable",
            kind="stepup",
            scopes=["outbound:enable"],
            expires_at=expires(minutes=5),
        )
    )
    await record_audit(session, event="auth.step_up", user_id=user.id, actor=user)
    return {"ok": True, "token": raw, "expires_in": 300}
