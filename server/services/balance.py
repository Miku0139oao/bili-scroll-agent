from __future__ import annotations

import os
from urllib.parse import urlparse

import aiohttp
from sqlalchemy.ext.asyncio import AsyncSession

from ..crypto import SecretCipher
from ..models import BalanceSnapshot, ProviderAccount, now_utc


class BalanceError(RuntimeError):
    pass


async def refresh_deepseek_balance(
    session: AsyncSession,
    cipher: SecretCipher,
    *,
    user_id: str,
) -> dict:
    account = await session.get(ProviderAccount, "deepseek")
    base_url = (
        os.environ.get("DEEPSEEK_BASE_URL")
        or (account.base_url if account else "")
        or "https://api.deepseek.com"
    ).rstrip("/")
    _validate_base_url(base_url)
    api_key = os.environ.get("DEEPSEEK_API_KEY", "").strip()
    if not api_key and account and account.api_key_enc:
        api_key = cipher.decrypt_bytes(account.api_key_enc, aad=b"provider:deepseek").decode("utf-8")
    if not api_key:
        raise BalanceError(
            "DeepSeek API key is not configured. Set DEEPSEEK_API_KEY or save it in Admin → Providers."
        )
    try:
        status, payload = await fetch_deepseek_balance(base_url, api_key)
    except TimeoutError as exc:
        raise BalanceError("DeepSeek balance request timed out") from exc
    except aiohttp.ClientError as exc:
        raise BalanceError(f"DeepSeek network error: {type(exc).__name__}") from exc
    if status == 401:
        raise BalanceError("DeepSeek rejected the API key (HTTP 401)")
    if status >= 400:
        message = payload.get("error", {}).get("message") if isinstance(payload, dict) else ""
        raise BalanceError(f"DeepSeek balance request failed (HTTP {status}): {message}")
    data = payload.get("balance_infos") or []
    if not isinstance(data, list):
        raise BalanceError("DeepSeek returned an invalid balance payload")
    threshold = float(account.low_balance_threshold if account else 10.0)
    fetched_at = now_utc()
    balances = []
    for item in data:
        total = _float(item.get("total_balance"))
        granted = _float(item.get("granted_balance"))
        topped_up = _float(item.get("topped_up_balance"))
        currency = str(item.get("currency") or "CNY")
        session.add(
            BalanceSnapshot(
                user_id=user_id,
                provider_id="deepseek",
                currency=currency,
                total_balance=total,
                granted_balance=granted,
                topped_up_balance=topped_up,
                is_available=bool(payload.get("is_available", True)),
                low_balance=total < threshold,
                source="deepseek-native",
                fetched_at=fetched_at,
            )
        )
        balances.append(
            {
                "provider": "deepseek",
                "currency": currency,
                "total": total,
                "granted": granted,
                "topped_up": topped_up,
                "available": bool(payload.get("is_available", True)),
                "low": total < threshold,
                "source": "deepseek-native",
                "fetched_at": fetched_at,
            }
        )
    return {
        "provider": "deepseek",
        "base_url": base_url,
        "threshold": threshold,
        "balances": balances,
    }


async def fetch_deepseek_balance(base_url: str, api_key: str) -> tuple[int, dict]:
    timeout = aiohttp.ClientTimeout(total=20)
    headers = {
        "Authorization": f"Bearer {api_key}",
        "User-Agent": "bili-scroll-agent/0.1",
        "Accept": "application/json",
    }
    async with aiohttp.ClientSession(timeout=timeout) as client:
        async with client.get(f"{base_url}/user/balance", headers=headers) as response:
            return response.status, await response.json(content_type=None)


def provider_view(account: ProviderAccount | None) -> dict:
    if account:
        return {
            "provider_id": account.provider_id,
            "base_url": account.base_url,
            "has_api_key": bool(account.api_key_enc),
            "low_balance_threshold": account.low_balance_threshold,
            "enabled": account.enabled,
            "updated_at": account.updated_at,
        }
    return {
        "provider_id": "deepseek",
        "base_url": "https://api.deepseek.com",
        "has_api_key": bool(os.environ.get("DEEPSEEK_API_KEY")),
        "low_balance_threshold": 10,
        "enabled": True,
        "updated_at": None,
    }


def _validate_base_url(value: str) -> None:
    parsed = urlparse(value)
    if parsed.scheme not in {"https", "http"} or not parsed.hostname:
        raise BalanceError("DeepSeek base URL must be an absolute HTTP(S) URL")
    if parsed.username or parsed.password:
        raise BalanceError("DeepSeek base URL must not contain credentials")
    if parsed.scheme != "https" and parsed.hostname not in {"127.0.0.1", "localhost", "::1"}:
        raise BalanceError("Non-HTTPS DeepSeek base URL is only allowed on loopback")
    if parsed.path not in {"", "/"}:
        raise BalanceError("DeepSeek base URL must not include a path")


def _float(value) -> float:  # noqa: ANN001
    try:
        return max(0.0, float(value or 0))
    except (TypeError, ValueError):
        return 0.0
