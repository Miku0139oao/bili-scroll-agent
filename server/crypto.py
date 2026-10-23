from __future__ import annotations

import base64
import hashlib
import hmac
import json
import os
import secrets
from pathlib import Path
from typing import Any

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

from .config import Settings


class CryptoError(RuntimeError):
    pass


class SecretCipher:
    """Small envelope used for TOTP secrets and Bilibili cookies."""

    def __init__(self, settings: Settings):
        self._key = self._load_master_key(settings)
        self._aead = AESGCM(self._key)

    @staticmethod
    def _load_master_key(settings: Settings) -> bytes:
        raw = (settings.master_key or "").strip()
        if raw:
            try:
                key = base64.b64decode(raw, validate=True)
            except Exception as exc:  # noqa: BLE001
                raise CryptoError("BILI_SCROLL_MASTER_KEY must be base64") from exc
            if len(key) != 32:
                raise CryptoError("BILI_SCROLL_MASTER_KEY must decode to exactly 32 bytes")
            return key
        if settings.is_production:
            raise CryptoError("BILI_SCROLL_MASTER_KEY is required in production")
        key_file = settings.data_dir / "master.key"
        if key_file.exists():
            return base64.b64decode(key_file.read_text(encoding="ascii").strip(), validate=True)
        key = secrets.token_bytes(32)
        key_file.write_text(base64.b64encode(key).decode("ascii"), encoding="ascii")
        try:
            os.chmod(key_file, 0o600)
        except OSError:
            pass
        return key

    def encrypt_bytes(self, value: bytes, *, aad: bytes = b"bili-scroll") -> str:
        nonce = secrets.token_bytes(12)
        encrypted = self._aead.encrypt(nonce, value, aad)
        return "v1." + base64.urlsafe_b64encode(nonce + encrypted).decode("ascii")

    def decrypt_bytes(self, value: str, *, aad: bytes = b"bili-scroll") -> bytes:
        if not value.startswith("v1."):
            raise CryptoError("unsupported ciphertext version")
        packed = base64.urlsafe_b64decode(value[3:].encode("ascii"))
        if len(packed) < 29:
            raise CryptoError("ciphertext is truncated")
        return self._aead.decrypt(packed[:12], packed[12:], aad)

    def encrypt_json(self, value: Any, *, aad: bytes = b"bili-scroll") -> str:
        raw = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        return self.encrypt_bytes(raw, aad=aad)

    def decrypt_json(self, value: str, *, aad: bytes = b"bili-scroll") -> Any:
        return json.loads(self.decrypt_bytes(value, aad=aad).decode("utf-8"))

    def token_hash(self, token: str) -> str:
        return hmac.new(self._key, token.encode("utf-8"), hashlib.sha256).hexdigest()

    def stable_hash(self, value: str) -> str:
        return hmac.new(self._key, value.encode("utf-8"), hashlib.sha256).hexdigest()


def new_secret(prefix: str, entropy: int = 32) -> str:
    return f"{prefix}_{secrets.token_urlsafe(entropy)}"


def ensure_private_file(path: Path) -> None:
    try:
        os.chmod(path, 0o600)
    except OSError:
        pass


def constant_time_equal(a: str, b: str) -> bool:
    return hmac.compare_digest(a.encode("utf-8"), b.encode("utf-8"))
