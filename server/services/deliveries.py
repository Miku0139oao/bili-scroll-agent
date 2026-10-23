from __future__ import annotations

from datetime import UTC, datetime, timedelta

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..config import Settings
from ..crypto import SecretCipher
from ..models import Delivery, Run, User, VideoRecord, now_utc
from ..schemas import DeliveryDraft, PickedItem
from .bilibili import BilibiliService
from .config import load_admin_settings


async def process_deliveries(
    session: AsyncSession,
    cipher: SecretCipher,
    settings: Settings,
    *,
    user: User,
    run: Run,
    config: dict,
    picked: list[PickedItem],
    delivery_drafts: list[DeliveryDraft],
) -> list[dict]:
    admin = await load_admin_settings(session)
    if not settings.bind_outbound_enabled or not admin.get("outbound_enabled", True):
        return []
    if not picked:
        return []
    bili = BilibiliService(session, cipher)
    results: list[dict] = []

    comment_on = bool(
        config.get("autoComment")
        and config.get("mentionName")
        and config.get("dmMode") != "only"
        and int(config.get("commentPerRound") or 0) > 0
    )
    if comment_on:
        comments = [d for d in delivery_drafts if d.kind == "comment"]
        if not comments:
            comments = [
                DeliveryDraft(
                    kind="comment",
                    bvid=item.bvid,
                    text=_with_mention(
                        item.comment_text or item.one_liner or item.reason,
                        str(config.get("mentionName") or ""),
                    ),
                )
                for item in picked
            ]
        for draft in comments[: int(config.get("commentPerRound") or 0)]:
            if not draft.bvid:
                continue
            text = _with_mention(draft.text, str(config.get("mentionName") or ""))[:150]
            if await _existing_delivery(session, user.id, run.id, draft.bvid, "comment"):
                continue
            delivery = Delivery(
                user_id=user.id,
                run_id=run.id,
                bvid=draft.bvid,
                kind="comment",
                content=text,
                status="sending",
            )
            session.add(delivery)
            await session.flush()
            delivery.attempts += 1
            try:
                result = await bili.send_comment(user.id, draft.bvid, text)
                delivery.status = "sent"
                delivery.remote_id = str(result.get("rpid") or result.get("rpid_str") or "")
                delivery.root_id = str(result.get("root") or "")
                delivery.sent_at = now_utc()
                results.append(
                    {"kind": "comment", "bvid": draft.bvid, "ok": True, "rpid": delivery.remote_id}
                )
            except Exception as exc:  # noqa: BLE001
                delivery.status = "failed"
                delivery.error = str(exc)[:1000]
                results.append({"kind": "comment", "bvid": draft.bvid, "ok": False, "error": str(exc)})

    dm_mode = str(config.get("dmMode") or "off")
    dm_uid = int(config.get("dmUid") or 0)
    if dm_mode in {"only", "both"} and dm_uid > 0:
        draft_text = "\n\n".join(
            draft.text for draft in delivery_drafts if draft.kind == "dm" and draft.text.strip()
        )
        if not draft_text:
            draft_text = "\n\n".join(
                f"{item.title or item.bvid}\n{item.url or 'https://www.bilibili.com/video/' + item.bvid}\n{item.one_liner or item.reason}"
                for item in picked
            )
        draft_text = _fit_dm(draft_text)
        if not await _existing_delivery(session, user.id, run.id, "", "dm"):
            delivery = Delivery(
                user_id=user.id,
                run_id=run.id,
                bvid="",
                kind="dm",
                content=draft_text,
                status="sending",
            )
            session.add(delivery)
            await session.flush()
            delivery.attempts += 1
            try:
                result = await bili.send_dm(user.id, dm_uid, draft_text)
                delivery.status = "sent"
                delivery.remote_id = str(result.get("msg_key") or "")
                delivery.sent_at = now_utc()
                results.append({"kind": "dm", "ok": True})
            except Exception as exc:  # noqa: BLE001
                if len(draft_text) > 260:
                    shorter = _fit_dm(_simplify_dm(picked), limit=480)
                    delivery.content = shorter
                    delivery.attempts += 1
                    try:
                        result = await bili.send_dm(user.id, dm_uid, shorter)
                        delivery.status = "sent"
                        delivery.remote_id = str(result.get("msg_key") or "")
                        delivery.sent_at = now_utc()
                        results.append({"kind": "dm", "ok": True, "retried": True})
                    except Exception as retry_exc:  # noqa: BLE001
                        delivery.status = "failed"
                        delivery.error = str(retry_exc)[:1000]
                        results.append({"kind": "dm", "ok": False, "error": str(retry_exc)})
                else:
                    delivery.status = "failed"
                    delivery.error = str(exc)[:1000]
                    results.append({"kind": "dm", "ok": False, "error": str(exc)})

    fav_folder = int(config.get("favFolderId") or 0)
    if fav_folder > 0:
        for item in picked:
            try:
                await bili.favorite_add(user.id, item.bvid, fav_folder)
                results.append({"kind": "favorite", "bvid": item.bvid, "ok": True})
            except Exception as exc:  # noqa: BLE001
                results.append({"kind": "favorite", "bvid": item.bvid, "ok": False, "error": str(exc)})

    if results:
        delivered_ids = {
            result["bvid"] for result in results if result.get("ok") and result.get("kind") == "comment"
        }
        for bvid in delivered_ids:
            row = await session.scalar(
                select(VideoRecord).where(VideoRecord.user_id == user.id, VideoRecord.bvid == bvid)
            )
            if row:
                row.delivered = True
    return results


async def verify_recent(
    session: AsyncSession,
    cipher: SecretCipher,
    *,
    user_id: str,
    max_items: int = 10,
) -> list[dict]:
    """Check that sent comments are present in public comment pages."""
    rows = (
        await session.scalars(
            select(Delivery)
            .where(
                Delivery.user_id == user_id,
                Delivery.kind == "comment",
                Delivery.status == "sent",
                Delivery.verified_at.is_(None),
            )
            .order_by(Delivery.sent_at.desc())
            .limit(max_items)
        )
    ).all()
    bili = BilibiliService(session, cipher)
    results = []
    for row in rows:
        try:
            page = await bili.comments(user_id, row.bvid, num=50)
            visible = any(str(item.get("rpid")) == row.remote_id for item in page["comments"])
            row.visible = visible
            row.verified_at = now_utc()
            row.status = "verified" if visible else "unverified"
            results.append({"id": row.id, "bvid": row.bvid, "visible": visible})
        except Exception as exc:  # noqa: BLE001
            row.error = str(exc)[:1000]
            results.append({"id": row.id, "bvid": row.bvid, "visible": None, "error": str(exc)})
    return results


async def dm_reply_quota(session: AsyncSession, user_id: str) -> int:
    since = datetime.now(UTC) - timedelta(days=1)
    return int(
        await session.scalar(
            select(func.count())
            .select_from(Delivery)
            .where(
                Delivery.user_id == user_id,
                Delivery.kind == "dm_reply",
                Delivery.created_at >= since,
            )
        )
        or 0
    )


async def _existing_delivery(session: AsyncSession, user_id: str, run_id: str, bvid: str, kind: str) -> bool:
    return bool(
        await session.scalar(
            select(Delivery.id).where(
                Delivery.user_id == user_id,
                Delivery.run_id == run_id,
                Delivery.bvid == bvid,
                Delivery.kind == kind,
            )
        )
    )


def _with_mention(text: str, mention: str) -> str:
    value = text.strip()
    token = f"@{mention}".strip()
    return value if token in value else f"{token} {value}".strip()


def _fit_dm(text: str, limit: int = 500) -> str:
    value = text.strip()
    return value if len(value) <= limit else value[: limit - 1].rstrip() + "…"


def _simplify_dm(picked: list[PickedItem]) -> str:
    return "\n".join(
        f"{item.title or item.bvid}\n{item.url or 'https://www.bilibili.com/video/' + item.bvid}"
        for item in picked[:4]
    )
