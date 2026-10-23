from __future__ import annotations

from http.cookies import SimpleCookie

from server.services import bilibili


def test_poll_login_extracts_set_cookie_values(monkeypatch):
    class Response:
        status = 200
        cookies = SimpleCookie()

        def __init__(self):
            self.cookies.load(
                "SESSDATA=session-token; Path=/; "
                "bili_jct=csrf-token; Path=/; "
                "DedeUserID=12345; Path=/; buvid3=fingerprint; Path=/"
            )

        async def json(self, content_type=None):  # noqa: ANN001, ARG002
            return {"data": {"code": 0}}

        async def __aenter__(self):
            return self

        async def __aexit__(self, *_args):
            return None

    class Client:
        def __init__(self, timeout):  # noqa: ANN001, ARG002
            pass

        def get(self, *_args, **_kwargs):
            return Response()

        async def __aenter__(self):
            return self

        async def __aexit__(self, *_args):
            return None

    monkeypatch.setattr(bilibili.aiohttp, "ClientSession", Client)
    result = __import__("asyncio").run(bilibili.poll_login("test-key"))
    assert result["state"] == "ok"
    assert result["credential"]["sessdata"] == "session-token"
    assert result["credential"]["bili_jct"] == "csrf-token"
    assert result["credential"]["dedeuserid"] == "12345"
    assert result["credential"]["buvid3"] == "fingerprint"
