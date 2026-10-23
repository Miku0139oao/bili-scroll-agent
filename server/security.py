from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pyotp
from argon2 import PasswordHasher
from argon2.exceptions import InvalidHashError, VerificationError, VerifyMismatchError

PH = PasswordHasher(time_cost=3, memory_cost=65536, parallelism=4, hash_len=32, salt_len=16)


def hash_password(password: str) -> str:
    if len(password) < 12:
        raise ValueError("password must contain at least 12 characters")
    return PH.hash(password)


def verify_password(password_hash: str | None, password: str) -> bool:
    if not password_hash:
        return False
    try:
        return PH.verify(password_hash, password)
    except (VerifyMismatchError, VerificationError, InvalidHashError):
        return False


def password_needs_rehash(password_hash: str) -> bool:
    try:
        return PH.check_needs_rehash(password_hash)
    except InvalidHashError:
        return True


def new_totp_secret() -> str:
    return pyotp.random_base32()


def totp_uri(secret: str, username: str, issuer: str = "Bili Scroll Agent") -> str:
    return pyotp.TOTP(secret).provisioning_uri(name=username, issuer_name=issuer)


def verify_totp(secret: str | None, code: str) -> bool:
    if not secret or not code:
        return False
    return bool(pyotp.TOTP(secret).verify(code.strip().replace(" ", ""), valid_window=1))


def utcnow() -> datetime:
    return datetime.now(UTC)


def expires(hours: int = 0, minutes: int = 0) -> datetime:
    return utcnow() + timedelta(hours=hours, minutes=minutes)
