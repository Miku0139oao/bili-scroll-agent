from __future__ import annotations

import asyncio

import pyotp

from server.main import cipher, db
from server.services.auth import create_admin


def _create_admin(username: str = "admin", password: str = "correct horse battery") -> str:
    async def run():
        await db.initialize()
        async with db.session() as session:
            user, secret, _uri = await create_admin(
                session,
                cipher,
                username=username,
                display_name="Admin",
                password=password,
            )
            return secret

    return asyncio.run(run())


def _login(client, username: str, secret: str) -> str:
    response = client.post(
        "/api/auth/login",
        json={
            "username": username,
            "password": "correct horse battery",
            "totp": pyotp.TOTP(secret).now(),
        },
    )
    assert response.status_code == 200, response.text
    return response.json()["csrf"]


def test_invite_totp_and_tenant_isolation(client):
    admin_secret = _create_admin("admin-tenant")
    csrf = _login(client, "admin-tenant", admin_secret)
    invite_response = client.post(
        "/api/admin/invites",
        headers={"X-CSRF-Token": csrf},
        json={"username": "alice", "display_name": "Alice", "role": "member"},
    )
    assert invite_response.status_code == 200, invite_response.text
    invite_url = invite_response.json()["invite_url"]
    invite_token = invite_url.rsplit("/", 1)[-1]
    invite_info = client.get(f"/api/auth/invite/{invite_token}")
    assert invite_info.status_code == 200, invite_info.text
    assert invite_info.json()["username"] == "alice"

    accept = client.post(
        "/api/auth/invite/accept",
        json={"token": invite_token, "password": "a second strong pass"},
    )
    assert accept.status_code == 200, accept.text
    setup = accept.json()
    verify = client.post(
        "/api/auth/invite/verify",
        json={
            "setup_token": setup["setup_token"],
            "totp": pyotp.TOTP(setup["secret"]).now(),
        },
    )
    assert verify.status_code == 200, verify.text
    alice_id = verify.json()["user"]["id"]
    login = client.post(
        "/api/auth/login",
        json={
            "username": "alice",
            "password": "a second strong pass",
            "totp": pyotp.TOTP(setup["secret"]).now(),
        },
    )
    assert login.status_code == 200, login.text
    status = client.get("/api/me/status")
    assert status.status_code == 200
    assert status.json()["user"]["id"] == alice_id
    denied = client.get("/api/admin/users")
    assert denied.status_code == 403


def test_mutating_endpoint_requires_csrf(client):
    secret = _create_admin("owner")
    _login(client, "owner", secret)
    response = client.patch("/api/me/config", json={"perDay": 3})
    assert response.status_code == 403


def test_password_hash_and_secret_are_never_returned(client):
    secret = _create_admin("privacy")
    _login(client, "privacy", secret)
    response = client.get("/api/me/status")
    body = response.text
    assert "password_hash" not in body
    assert "totp_secret" not in body
    assert secret not in body
