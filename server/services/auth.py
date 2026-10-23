from __future__ import annotations

from datetime import UTC, datetime, timedelta

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..config import Settings
from ..crypto import SecretCipher, new_secret
from ..models import ApiToken, AuthSession, Invite, User
from ..schemas import MemberConfigPatch
from ..security import (
    hash_password,
    new_totp_secret,
    totp_uri,
    utcnow,
    verify_password,
    verify_totp,
)
from ..services.config import save_config


class AuthError(ValueError):
    pass


async def create_admin(
    session: AsyncSession,
    cipher: SecretCipher,
    *,
    username: str,
    display_name: str,
    password: str,
) -> tuple[User, str, str]:
    username = username.strip().lower()
    if not username:
        raise AuthError("username is required")
    existing = await session.scalar(select(User).where(User.username == username))
    if existing:
        raise AuthError("username already exists")
    secret = new_totp_secret()
    user = User(
        username=username,
        display_name=display_name.strip() or username,
        role="admin",
        password_hash=hash_password(password),
        totp_secret_enc=cipher.encrypt_bytes(secret.encode("ascii"), aad=f"totp:{username}".encode()),
        totp_confirmed=True,
    )
    session.add(user)
    await session.flush()
    await save_config(session, user.id, MemberConfigPatch())
    return user, secret, totp_uri(secret, username)


async def create_invite(
    session: AsyncSession,
    cipher: SecretCipher,
    settings: Settings,
    *,
    created_by: str,
    username: str,
    display_name: str,
    role: str,
) -> tuple[Invite, str]:
    username = username.strip().lower()
    if await session.scalar(select(User.id).where(User.username == username)):
        raise AuthError("username already exists")
    now = utcnow()
    await session.execute(select(Invite).where(Invite.username == username, Invite.used_at.is_(None)))
    token = new_secret("bili_invite", 32)
    invite = Invite(
        token_hash=cipher.token_hash(token),
        username=username,
        display_name=display_name.strip() or username,
        role=role,
        created_by=created_by,
        expires_at=now + timedelta(hours=settings.invite_hours),
    )
    session.add(invite)
    await session.flush()
    return invite, token


async def accept_invite(
    session: AsyncSession,
    cipher: SecretCipher,
    *,
    token: str,
    password: str,
) -> dict:
    invite = await session.scalar(
        select(Invite).where(
            Invite.token_hash == cipher.token_hash(token),
            Invite.used_at.is_(None),
        )
    )
    now = utcnow()
    if not invite or _utc(invite.expires_at) <= now:
        raise AuthError("invite is invalid or expired")
    if await session.scalar(select(User.id).where(User.username == invite.username)):
        raise AuthError("username already exists")
    secret = new_totp_secret()
    user = User(
        username=invite.username,
        display_name=invite.display_name,
        role=invite.role,
        password_hash=hash_password(password),
        totp_secret_enc=cipher.encrypt_bytes(secret.encode("ascii"), aad=f"totp:{invite.username}".encode()),
        totp_confirmed=False,
    )
    invite.used_at = now
    session.add(user)
    await session.flush()
    await save_config(session, user.id, MemberConfigPatch())
    setup_token = new_secret("bili_setup", 32)
    session.add(
        ApiToken(
            id_hash=cipher.token_hash(setup_token),
            user_id=user.id,
            label="invite-totp-setup",
            kind="setup",
            scopes=["totp:confirm"],
            expires_at=now + timedelta(minutes=20),
        )
    )
    return {
        "setup_token": setup_token,
        "secret": secret,
        "otpauth_uri": totp_uri(secret, user.username),
    }


async def confirm_invite_totp(
    session: AsyncSession,
    cipher: SecretCipher,
    *,
    setup_token: str,
    code: str,
) -> User:
    token_row = await session.scalar(
        select(ApiToken).where(
            ApiToken.id_hash == cipher.token_hash(setup_token),
            ApiToken.kind == "setup",
            ApiToken.revoked_at.is_(None),
        )
    )
    if not token_row or (token_row.expires_at and _utc(token_row.expires_at) <= utcnow()):
        raise AuthError("setup token is invalid or expired")
    user = await session.get(User, token_row.user_id)
    if not user or user.totp_confirmed or not user.totp_secret_enc:
        raise AuthError("setup session is invalid")
    secret = cipher.decrypt_bytes(user.totp_secret_enc, aad=f"totp:{user.username}".encode()).decode("ascii")
    if not verify_totp(secret, code):
        raise AuthError("invalid TOTP code")
    user.totp_confirmed = True
    token_row.revoked_at = utcnow()
    return user


async def authenticate_user(
    session: AsyncSession,
    cipher: SecretCipher,
    *,
    username: str,
    password: str,
    totp: str,
) -> User:
    user = await session.scalar(select(User).where(User.username == username.strip().lower()))
    if not user or user.disabled or not user.totp_confirmed:
        raise AuthError("invalid credentials")
    if not verify_password(user.password_hash, password):
        raise AuthError("invalid credentials")
    if not user.totp_secret_enc:
        raise AuthError("invalid credentials")
    secret = cipher.decrypt_bytes(user.totp_secret_enc, aad=f"totp:{user.username}".encode()).decode("ascii")
    if not verify_totp(secret, totp):
        raise AuthError("invalid credentials")
    user.last_login_at = utcnow()
    return user


async def create_session(
    request_ip: str | None,
    session: AsyncSession,
    cipher: SecretCipher,
    settings: Settings,
    user: User,
) -> tuple[str, str]:
    now = utcnow()
    session_token = new_secret("bili_sess", 32)
    csrf_token = new_secret("bili_csrf", 24)
    row = AuthSession(
        id_hash=cipher.token_hash(session_token),
        user_id=user.id,
        csrf_hash=cipher.token_hash(csrf_token),
        expires_at=now + timedelta(hours=settings.session_hours),
        idle_expires_at=now + timedelta(minutes=settings.session_idle_minutes),
        ip_hint=request_ip,
    )
    session.add(row)
    return session_token, csrf_token


async def revoke_session(session: AsyncSession, row: AuthSession) -> None:
    row.revoked_at = utcnow()


async def count_admins(session: AsyncSession) -> int:
    return int(await session.scalar(select(func.count()).select_from(User).where(User.role == "admin")))


def _utc(value: datetime) -> datetime:
    return value if value.tzinfo else value.replace(tzinfo=UTC)
