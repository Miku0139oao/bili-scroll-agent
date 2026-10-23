from __future__ import annotations

import argparse
import asyncio
import getpass
import shutil
import sqlite3
from datetime import UTC, datetime
from pathlib import Path

import uvicorn
from sqlalchemy import select
from sqlalchemy import update as sql_update

from .config import get_settings
from .crypto import SecretCipher
from .db import Database
from .models import ApiToken, AuthSession, User, now_utc
from .security import new_totp_secret, totp_uri
from .services.auth import create_admin
from .services.runners import runner_status


async def _create_admin(args: argparse.Namespace) -> None:
    settings = get_settings()
    db = Database(settings)
    await db.initialize()
    cipher = SecretCipher(settings)
    username = args.username or input("Admin username: ").strip().lower()
    display_name = args.display_name or input("Display name: ").strip() or username
    password = args.password or getpass.getpass("Password (12+ chars): ")
    async with db.session() as session:
        user, secret, uri = await create_admin(
            session,
            cipher,
            username=username,
            display_name=display_name,
            password=password,
        )
        print(f"Created admin: {user.username}")
        print(f"TOTP secret: {secret}")
        print(f"TOTP URI: {uri}")
        print("Open the URI in your authenticator, then log in and confirm the code.")
    await db.close()


async def _backup(_args: argparse.Namespace) -> None:
    settings = get_settings()
    source = Path(settings.data_dir) / "bili-scroll.db"
    target_dir = Path(settings.data_dir) / "backups"
    target_dir.mkdir(parents=True, exist_ok=True)
    target = target_dir / f"bili-scroll-{datetime.now(UTC).strftime('%Y%m%d-%H%M%S')}.db"
    if source.exists():
        with sqlite3.connect(source) as src, sqlite3.connect(target) as dst:
            src.backup(dst)
        print(target)
    else:
        raise SystemExit(f"database not found: {source}")


async def _doctor(_args: argparse.Namespace) -> None:
    settings = get_settings()
    print(f"data_dir={settings.data_dir}")
    print(f"database={settings.database_url}")
    for name, info in runner_status(settings).items():
        print(f"{name}: {'available' if info['available'] else 'missing'} ({info['command']})")
    print(f"ffmpeg={'available' if shutil.which('ffmpeg') else 'missing'}")


async def _reset_totp(args: argparse.Namespace) -> None:
    settings = get_settings()
    db = Database(settings)
    await db.initialize()
    cipher = SecretCipher(settings)
    username = (args.username or input("Username: ")).strip().lower()
    async with db.session() as session:
        user = await session.scalar(select(User).where(User.username == username))
        if not user:
            raise SystemExit(f"user not found: {username}")
        secret = new_totp_secret()
        user.totp_secret_enc = cipher.encrypt_bytes(
            secret.encode("ascii"), aad=f"totp:{user.username}".encode()
        )
        user.totp_confirmed = True
        await session.execute(
            sql_update(AuthSession)
            .where(AuthSession.user_id == user.id)
            .values(revoked_at=now_utc())
        )
        await session.execute(
            sql_update(ApiToken)
            .where(ApiToken.user_id == user.id, ApiToken.kind.in_(["stepup", "setup"]))
            .values(revoked_at=now_utc())
        )
        uri = totp_uri(secret, user.username)
        print(f"Reset TOTP for: {user.username}")
        print(f"TOTP secret: {secret}")
        print(f"TOTP URI: {uri}")
        print("Existing browser sessions have been revoked.")
    await db.close()


def main() -> None:
    parser = argparse.ArgumentParser(prog="bili-scroll")
    sub = parser.add_subparsers(dest="command")
    serve = sub.add_parser("serve")
    serve.add_argument("--host")
    serve.add_argument("--port", type=int)
    admin = sub.add_parser("create-admin")
    admin.add_argument("--username")
    admin.add_argument("--display-name")
    admin.add_argument("--password")
    sub.add_parser("backup")
    sub.add_parser("doctor")
    reset = sub.add_parser("reset-totp")
    reset.add_argument("--username")
    args = parser.parse_args()
    if args.command == "create-admin":
        asyncio.run(_create_admin(args))
        return
    if args.command == "backup":
        asyncio.run(_backup(args))
        return
    if args.command == "doctor":
        asyncio.run(_doctor(args))
        return
    if args.command == "reset-totp":
        asyncio.run(_reset_totp(args))
        return
    settings = get_settings()
    uvicorn.run(
        "server.main:app",
        host=args.host or getattr(args, "host", None) or settings.host,
        port=args.port or getattr(args, "port", None) or settings.port,
        reload=False,
    )


if __name__ == "__main__":
    main()
