from __future__ import annotations

import asyncio

from sqlalchemy import select

from server.main import cipher, db, settings
from server.models import BiliCredential, ProviderAccount, Report, User, VideoRecord
from server.schemas import PickedItem, RunSummary
from server.services.auth import create_admin
from server.services.balance import refresh_deepseek_balance
from server.services.runs import (
    activate_run,
    count_picked,
    create_run,
    finish_run,
    local_day,
    record_candidates,
)


async def _user(username: str) -> User:
    await db.initialize()
    async with db.session() as session:
        await create_admin(
            session,
            cipher,
            username=username,
            display_name=username,
            password="a correct horse battery",
        )
        user = await session.scalar(select(User).where(User.username == username))
        assert user
        session.add(
            BiliCredential(
                user_id=user.id,
                ciphertext=cipher.encrypt_json(
                    {
                        "sessdata": "s",
                        "bili_jct": "j",
                        "buvid3": "b",
                        "dedeuserid": "42",
                    },
                    aad=f"bili:{user.id}".encode(),
                ),
                uid=42,
            )
        )
        return user


def test_finalize_writes_report_and_dedupes():
    async def scenario():
        user = await _user("report-owner")
        bvid = "BV1xx411c7mD"
        async with db.session() as session:
            user = await session.get(User, user.id)
            assert user
            run = await create_run(
                session,
                cipher,
                settings,
                user=user,
                kind="manual",
                runner="codex",
            )
            await activate_run(session, run)
            await record_candidates(
                session,
                run_id=run.id,
                user_id=user.id,
                videos=[
                    {
                        "bvid": bvid,
                        "title": "Real subtitle-based pick",
                        "author": "Tester",
                        "url": f"https://www.bilibili.com/video/{bvid}",
                    }
                ],
            )
            result = await finish_run(
                session,
                cipher,
                settings,
                user=user,
                run=run,
                summary=RunSummary(
                    run_id=run.id,
                    picked=[
                        PickedItem(
                            bvid=bvid,
                            title="Real subtitle-based pick",
                            author="Tester",
                            tier="值得看",
                            reason="It contains concrete implementation details.",
                            one_liner="A useful walkthrough.",
                        )
                    ],
                    summary="One useful video.",
                    no_subtitle_count=2,
                ),
            )
            assert result["picked"] == 1
            report = await session.scalar(
                select(Report).where(Report.user_id == user.id, Report.day == local_day({"schedule": {}}))
            )
            assert report
            assert f"### {bvid}" in report.markdown
            assert "**檔位**：值得看" in report.markdown
            assert "因無字幕跳過 2 條" in report.markdown
            records = (await session.scalars(select(VideoRecord).where(VideoRecord.user_id == user.id))).all()
            assert len(records) == 1
            assert await count_picked(session, user.id, local_day({"schedule": {}})) == 1

    asyncio.run(scenario())


def test_native_deepseek_balance(monkeypatch):
    async def fake_fetch(base_url: str, api_key: str):
        assert base_url == "https://api.deepseek.com"
        assert api_key == "sk-test"
        return 200, {
            "is_available": True,
            "balance_infos": [
                {
                    "currency": "CNY",
                    "total_balance": "19.50",
                    "granted_balance": "9.50",
                    "topped_up_balance": "10.00",
                }
            ],
        }

    monkeypatch.setenv("DEEPSEEK_API_KEY", "sk-test")
    import server.services.balance as balance_module

    monkeypatch.setattr(balance_module, "fetch_deepseek_balance", fake_fetch)

    async def scenario():
        user = await _user("balance-owner")
        async with db.session() as session:
            account = ProviderAccount(
                provider_id="deepseek",
                base_url="https://api.deepseek.com",
                low_balance_threshold=10,
                enabled=True,
            )
            await session.merge(account)
            result = await refresh_deepseek_balance(session, cipher, user_id=user.id)
            assert result["balances"][0]["total"] == 19.5
            assert result["balances"][0]["low"] is False

    asyncio.run(scenario())
