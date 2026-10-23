from __future__ import annotations

import asyncio

import pyotp

from server.main import cipher, db
from server.services.auth import create_admin


def _admin(username: str) -> str:
    async def run():
        await db.initialize()
        async with db.session() as session:
            _user, secret, _uri = await create_admin(
                session,
                cipher,
                username=username,
                display_name=username,
                password="a correct horse battery",
            )
            return secret

    return asyncio.run(run())


def _login_and_token(client, username: str) -> tuple[str, str]:
    secret = _admin(username)
    login = client.post(
        "/api/auth/login",
        json={
            "username": username,
            "password": "a correct horse battery",
            "totp": pyotp.TOTP(secret).now(),
        },
    )
    assert login.status_code == 200, login.text
    csrf = login.json()["csrf"]
    token_response = client.post(
        "/api/me/tokens",
        headers={"X-CSRF-Token": csrf},
        json={"label": "integration test"},
    )
    assert token_response.status_code == 200, token_response.text
    return csrf, token_response.json()["token"]


def _mcp(client, token: str, method: str, params: dict, request_id: int):
    return client.post(
        "/api/mcp",
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/json, text/event-stream",
        },
        json={
            "jsonrpc": "2.0",
            "id": request_id,
            "method": method,
            "params": params,
        },
    )


def test_mcp_initialize_and_status(client):
    _csrf, token = _login_and_token(client, "mcp-owner")
    initialize = _mcp(
        client,
        token,
        "initialize",
        {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "pytest", "version": "1"},
        },
        1,
    )
    assert initialize.status_code == 200, initialize.text
    listed = _mcp(client, token, "tools/list", {}, 2)
    assert listed.status_code == 200, listed.text
    names = {tool["name"] for tool in listed.json()["result"]["tools"]}
    assert "bili_status" in names
    assert "bili_finish_run" in names
    called = _mcp(
        client,
        token,
        "tools/call",
        {"name": "bili_status", "arguments": {}},
        3,
    )
    assert called.status_code == 200, called.text
    result = called.json()["result"]
    assert result.get("isError") is not True


def test_usage_import_and_dashboard(client):
    _csrf, token = _login_and_token(client, "usage-owner")
    response = client.post(
        "/api/ingest/usage",
        headers={"Authorization": f"Bearer {token}"},
        json={
            "source": "cc-switch",
            "rows": [
                {
                    "date": "2026-10-24",
                    "app_type": "codex",
                    "provider_id": "deepseek",
                    "model": "deepseek-flash",
                    "request_count": 4,
                    "success_count": 4,
                    "input_tokens": 1000,
                    "output_tokens": 200,
                    "cache_read_tokens": 800,
                    "total_cost_usd": 0.012,
                }
            ],
        },
    )
    assert response.status_code == 200, response.text
    dashboard = client.get("/api/me/usage?days=30")
    assert dashboard.status_code == 200, dashboard.text
    payload = dashboard.json()
    assert payload["totals"]["requests"] >= 4
    assert any(row["provider"] == "deepseek" for row in payload["providers"])


def test_native_balance_through_api(client, monkeypatch):
    csrf, _token = _login_and_token(client, "balance-api-owner")
    saved = client.put(
        "/api/admin/providers/deepseek",
        headers={"X-CSRF-Token": csrf},
        json={
            "base_url": "https://api.deepseek.com",
            "api_key": "sk-native-test",
            "low_balance_threshold": 20,
            "enabled": True,
        },
    )
    assert saved.status_code == 200, saved.text
    assert saved.json()["deepseek"]["has_api_key"] is True
    assert "sk-native-test" not in saved.text

    async def fake_fetch(_base_url: str, _api_key: str):
        return 200, {
            "is_available": True,
            "balance_infos": [
                {
                    "currency": "CNY",
                    "total_balance": "18.25",
                    "granted_balance": "8.25",
                    "topped_up_balance": "10.00",
                }
            ],
        }

    import server.services.balance as balance_module

    monkeypatch.setattr(balance_module, "fetch_deepseek_balance", fake_fetch)
    refreshed = client.post(
        "/api/me/balance/refresh",
        headers={"X-CSRF-Token": csrf},
    )
    assert refreshed.status_code == 200, refreshed.text
    assert refreshed.json()["balances"][0]["low"] is True
    dashboard = client.get("/api/me/usage?days=30")
    assert dashboard.status_code == 200
    assert dashboard.json()["balances"][0]["total"] == 18.25
