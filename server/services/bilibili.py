from __future__ import annotations

import asyncio
import base64
import io
import json
import logging
import re
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Any
from urllib.parse import quote

import aiohttp
import qrcode
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..crypto import SecretCipher
from ..models import BiliCredential

LOGGER = logging.getLogger(__name__)

UA = (
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
    "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
)
QR_GENERATE = "https://passport.bilibili.com/x/passport-login/web/qrcode/generate?source=main-fe-header"
QR_POLL = (
    "https://passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key={key}&source=main-fe-header"
)
SPI_URL = "https://api.bilibili.com/x/frontend/finger/spi"
DM_SESSIONS = (
    "https://api.vc.bilibili.com/session_svr/v1/session_svr/get_sessions"
    "?session_type=1&group_id=0&Build=0&mobi_app=web"
)
DM_MESSAGES = (
    "https://api.vc.bilibili.com/svr_sync/v1/svr_sync/fetch_session_msgs"
    "?talker_id={uid}&session_type=1&size={size}&build=0&mobi_app=web"
)
COMMENT_REPLIES = (
    "https://api.bilibili.com/x/v2/reply/reply"
    "?oid={aid}&type=1&root={root}&pn=1&ps=20"
)


class BiliError(RuntimeError):
    pass


class NotLoggedIn(BiliError):
    pass


@dataclass(slots=True)
class CredentialBundle:
    sessdata: str
    bili_jct: str
    buvid3: str
    dedeuserid: str

    @classmethod
    def from_mapping(cls, value: dict) -> CredentialBundle:
        return cls(
            sessdata=str(value.get("sessdata") or value.get("SESSDATA") or ""),
            bili_jct=str(value.get("bili_jct") or ""),
            buvid3=str(value.get("buvid3") or ""),
            dedeuserid=str(value.get("dedeuserid") or value.get("DedeUserID") or ""),
        )

    def as_dict(self) -> dict[str, str]:
        return {
            "sessdata": self.sessdata,
            "bili_jct": self.bili_jct,
            "buvid3": self.buvid3,
            "dedeuserid": self.dedeuserid,
        }

    def require(self) -> CredentialBundle:
        if not self.sessdata or not self.bili_jct or not self.dedeuserid:
            raise NotLoggedIn("Bilibili login is required")
        return self


class LoginBroker:
    def __init__(self) -> None:
        self._owners: dict[str, str] = {}
        self._lock = asyncio.Lock()

    async def claim(self, key: str, user_id: str) -> None:
        async with self._lock:
            self._owners[key] = user_id

    async def owner(self, key: str) -> str | None:
        async with self._lock:
            return self._owners.get(key)

    async def forget(self, key: str) -> None:
        async with self._lock:
            self._owners.pop(key, None)


login_broker = LoginBroker()


async def start_login(user_id: str) -> dict[str, str]:
    timeout = aiohttp.ClientTimeout(total=20)
    async with aiohttp.ClientSession(timeout=timeout) as client:
        async with client.get(QR_GENERATE, headers={"User-Agent": UA}) as response:
            payload = await response.json(content_type=None)
    data = payload.get("data") or {}
    key = str(data.get("qrcode_key") or "")
    url = str(data.get("url") or "")
    if not key or not url:
        raise BiliError(str(payload.get("message") or "could not generate QR code"))
    image = qrcode.make(url)
    buffer = io.BytesIO()
    image.save(buffer, format="PNG")
    await login_broker.claim(key, user_id)
    return {
        "key": key,
        "url": url,
        "qr": "data:image/png;base64," + base64.b64encode(buffer.getvalue()).decode("ascii"),
    }


async def poll_login(key: str) -> dict[str, Any]:
    timeout = aiohttp.ClientTimeout(total=20)
    async with (
        aiohttp.ClientSession(timeout=timeout) as client,
        client.get(
            QR_POLL.format(key=quote(key, safe="")),
            headers={"User-Agent": UA},
            allow_redirects=False,
        ) as response,
    ):
        payload = await response.json(content_type=None)
        cookies = {key: morsel.value for key, morsel in response.cookies.items()}
    code = (payload.get("data") or {}).get("code")
    if code == 86101:
        return {"state": "waiting", "code": code}
    if code == 86090:
        return {"state": "scanned", "code": code}
    if code == 86038:
        await login_broker.forget(key)
        return {"state": "expired", "code": code}
    if code != 0:
        return {
            "state": "unknown",
            "code": code,
            "message": str(payload.get("message") or ""),
        }
    credential = CredentialBundle(
        sessdata=str(cookies.get("SESSDATA") or ""),
        bili_jct=str(cookies.get("bili_jct") or ""),
        buvid3=str(cookies.get("buvid3") or ""),
        dedeuserid=str(cookies.get("DedeUserID") or ""),
    )
    if not credential.buvid3:
        credential.buvid3 = await _fetch_buvid3()
    if not credential.sessdata:
        return {
            "state": "unknown",
            "code": code,
            "message": "login completed but SESSDATA was missing",
        }
    await login_broker.forget(key)
    return {"state": "ok", "code": code, "credential": credential.as_dict()}


async def _fetch_buvid3() -> str:
    try:
        timeout = aiohttp.ClientTimeout(total=10)
        async with aiohttp.ClientSession(timeout=timeout) as client:
            async with client.get(SPI_URL, headers={"User-Agent": UA}) as response:
                payload = await response.json(content_type=None)
        return str((payload.get("data") or {}).get("b_3") or "")
    except Exception:  # noqa: BLE001
        return ""


class BilibiliService:
    def __init__(self, session: AsyncSession, cipher: SecretCipher):
        self.session = session
        self.cipher = cipher
        self._last_call = 0.0
        self._call_lock = asyncio.Lock()

    async def load_credential(self, user_id: str) -> CredentialBundle:
        row = await self.session.get(BiliCredential, user_id)
        if not row:
            raise NotLoggedIn("Bilibili login is required")
        raw = self.cipher.decrypt_json(row.ciphertext, aad=f"bili:{user_id}".encode())
        return CredentialBundle.from_mapping(raw).require()

    async def save_credential(
        self,
        user_id: str,
        raw: dict,
        *,
        uid: int | None = None,
        nickname: str | None = None,
    ) -> BiliCredential:
        credential = CredentialBundle.from_mapping(raw).require()
        row = await self.session.get(BiliCredential, user_id)
        if row is None:
            row = BiliCredential(user_id=user_id)
            self.session.add(row)
        row.ciphertext = self.cipher.encrypt_json(credential.as_dict(), aad=f"bili:{user_id}".encode())
        row.uid = uid
        if row.uid is None and credential.dedeuserid.isdigit():
            row.uid = int(credential.dedeuserid)
        row.nickname = nickname or row.nickname
        row.updated_at = datetime.now(UTC)
        return row

    def _api_credential(self, bundle: CredentialBundle):
        from bilibili_api import Credential

        return Credential(
            sessdata=bundle.sessdata,
            bili_jct=bundle.bili_jct,
            buvid3=bundle.buvid3,
            dedeuserid=bundle.dedeuserid,
        )

    async def whoami(self, user_id: str) -> dict[str, Any]:
        bundle = await self.load_credential(user_id)
        try:
            from bilibili_api import user

            data = await user.get_self_info(credential=self._api_credential(bundle))
            return {
                "ok": True,
                "uid": int(data.get("mid") or bundle.dedeuserid),
                "nickname": data.get("name") or "",
                "avatar": data.get("face") or "",
            }
        except Exception as exc:  # noqa: BLE001
            raise BiliError(f"credential check failed: {exc}") from exc

    async def _throttle(self) -> None:
        async with self._call_lock:
            now = asyncio.get_running_loop().time()
            delay = 0.34 - (now - self._last_call)
            if delay > 0:
                await asyncio.sleep(delay)
            self._last_call = asyncio.get_running_loop().time()

    async def search(
        self,
        user_id: str,
        keyword: str,
        *,
        num: int = 10,
        order: str = "pubdate",
        days: int = 7,
    ) -> dict[str, Any]:
        await self._throttle()
        from bilibili_api import search

        order_map = {
            "totalrank": search.OrderVideo.TOTALRANK,
            "click": search.OrderVideo.CLICK,
            "pubdate": search.OrderVideo.PUBDATE,
            "dm": search.OrderVideo.DM,
        }
        result = await search.search_by_type(
            keyword=keyword,
            search_type=search.SearchObjectType.VIDEO,
            page=1,
            order_type=order_map.get(order, search.OrderVideo.PUBDATE),
        )
        videos: list[dict[str, Any]] = []
        cutoff = datetime.now(UTC) - timedelta(days=days)
        for item in (result.get("result") or [])[: max(1, min(num, 50))]:
            title = re.sub(r"<[^>]+>", "", str(item.get("title") or ""))
            pub_ts = item.get("pubdate") or item.get("senddate")
            pubdate = _timestamp(pub_ts)
            if pubdate and pubdate < cutoff:
                continue
            videos.append(
                {
                    "bvid": item.get("bvid") or "",
                    "aid": item.get("aid") or 0,
                    "title": title,
                    "author": item.get("author") or "",
                    "play": _int(item.get("play")),
                    "review": _int(item.get("review")),
                    "danmaku": _int(item.get("video_review")),
                    "duration": item.get("duration") or "",
                    "description": str(item.get("description") or "")[:300],
                    "pubdate": pubdate.isoformat() if pubdate else None,
                    "cover": str(item.get("pic") or item.get("cover") or ""),
                    "url": f"https://www.bilibili.com/video/{item.get('bvid') or ''}",
                }
            )
        return {"keyword": keyword, "count": len(videos), "videos": videos}

    async def video_info(self, user_id: str, bvid: str) -> dict[str, Any]:
        await self._throttle()
        bundle = await self.load_credential(user_id)
        from bilibili_api import video

        data = await video.Video(bvid=bvid, credential=self._api_credential(bundle)).get_info()
        pubdate = _timestamp(data.get("pubdate"))
        stat = data.get("stat") or {}
        return {
            "bvid": bvid,
            "aid": data.get("aid"),
            "title": data.get("title") or "",
            "author": (data.get("owner") or {}).get("name") or "",
            "url": f"https://www.bilibili.com/video/{bvid}",
            "description": data.get("desc") or "",
            "cover": data.get("pic") or "",
            "duration": data.get("duration") or 0,
            "pubdate": pubdate.isoformat() if pubdate else None,
            "view": stat.get("view", 0),
            "danmaku": stat.get("danmaku", 0),
            "reply": stat.get("reply", 0),
            "favorite": stat.get("favorite", 0),
        }

    async def subtitle(self, user_id: str, bvid: str) -> dict[str, Any]:
        await self._throttle()
        bundle = await self.load_credential(user_id)
        from bilibili_api import video

        api_video = video.Video(bvid=bvid, credential=self._api_credential(bundle))
        info = await api_video.get_info()
        cid = info.get("cid") or ((info.get("pages") or [{}])[0].get("cid"))
        if not cid:
            return {"bvid": bvid, "empty": True, "message": "missing cid"}
        subtitle_data = await api_video.get_subtitle(cid=cid)
        subtitles = subtitle_data.get("subtitles") or []
        if not subtitles:
            return {"bvid": bvid, "empty": True, "message": "该视频没有字幕"}
        chosen = next(
            (item for item in subtitles if item.get("lan") in {"ai-zh", "zh-CN", "zh"}),
            subtitles[0],
        )
        url = str(chosen.get("subtitle_url") or "")
        if url.startswith("//"):
            url = "https:" + url
        timeout = aiohttp.ClientTimeout(total=30)
        async with aiohttp.ClientSession(timeout=timeout) as client:
            async with client.get(url, headers={"User-Agent": UA}) as response:
                payload = await response.json(content_type=None)
        parts = [str(item.get("content") or "") for item in payload.get("body") or []]
        text = "\n".join(part for part in parts if part).strip()
        return {
            "bvid": bvid,
            "title": info.get("title") or "",
            "language": chosen.get("lan_doc") or "",
            "segments": len(parts),
            "text": text,
            "empty": not text or _subtitle_is_byline(text),
        }

    async def comments(self, user_id: str, bvid: str, num: int = 30) -> dict[str, Any]:
        await self._throttle()
        bundle = await self.load_credential(user_id)
        from bilibili_api import comment, video

        api_video = video.Video(bvid=bvid, credential=self._api_credential(bundle))
        info = await api_video.get_info()
        rows: list[dict[str, Any]] = []
        page = 1
        while len(rows) < num:
            response = await comment.get_comments(
                oid=info["aid"],
                type_=comment.CommentResourceType.VIDEO,
                page_index=page,
                order=comment.OrderType.LIKE,
                credential=self._api_credential(bundle),
            )
            replies = response.get("replies") or []
            if not replies:
                break
            for reply in replies:
                rows.append(
                    {
                        "rpid": reply.get("rpid"),
                        "author": (reply.get("member") or {}).get("uname") or "",
                        "uid": (reply.get("member") or {}).get("mid") or 0,
                        "content": (reply.get("content") or {}).get("message") or "",
                        "like": _int(reply.get("like") or 0),
                        "reply_count": _int(reply.get("rcount") or 0),
                        "children": [
                            {
                                "rpid": child.get("rpid"),
                                "author": (child.get("member") or {}).get("uname") or "",
                                "content": (child.get("content") or {}).get("message") or "",
                            }
                            for child in ((reply.get("replies") or [])[:5])
                        ],
                    }
                )
                if len(rows) >= num:
                    break
            page += 1
            if page > 5:
                break
        return {"bvid": bvid, "count": len(rows), "comments": rows}

    async def favorite_folders(self, user_id: str) -> list[dict[str, Any]]:
        bundle = await self.load_credential(user_id)
        from bilibili_api import favorite_list

        data = await favorite_list.get_video_favorite_list(
            int(bundle.dedeuserid), credential=self._api_credential(bundle)
        )
        return [
            {
                "id": item.get("id"),
                "title": item.get("title"),
                "count": item.get("media_count", 0),
            }
            for item in data.get("list") or []
        ]

    async def comment_replies(
        self, user_id: str, bvid: str, root_rpid: int
    ) -> list[dict[str, Any]]:
        bundle = await self.load_credential(user_id)
        from bilibili_api import video

        info = await video.Video(
            bvid=bvid, credential=self._api_credential(bundle)
        ).get_info()
        url = COMMENT_REPLIES.format(aid=info["aid"], root=int(root_rpid))
        timeout = aiohttp.ClientTimeout(total=25)
        headers = {
            "User-Agent": UA,
            "Referer": f"https://www.bilibili.com/video/{bvid}",
            "Cookie": (
                f"SESSDATA={bundle.sessdata}; bili_jct={bundle.bili_jct}; "
                f"buvid3={bundle.buvid3}"
            ),
        }
        async with aiohttp.ClientSession(timeout=timeout) as client:
            async with client.get(url, headers=headers) as response:
                payload = await response.json(content_type=None)
        if payload.get("code") != 0:
            raise BiliError(
                f"comment reply lookup failed: code={payload.get('code')} "
                f"message={payload.get('message') or ''}"
            )
        rows = []
        for item in (payload.get("data") or {}).get("replies") or []:
            member = item.get("member") or {}
            content = (item.get("content") or {}).get("message") or ""
            rows.append(
                {
                    "rpid": int(item.get("rpid") or 0),
                    "root": int(item.get("root") or root_rpid),
                    "parent": int(item.get("parent") or root_rpid),
                    "uid": int(member.get("mid") or 0),
                    "author": member.get("uname") or "",
                    "text": str(content)[:500],
                    "ctime": int(item.get("ctime") or 0),
                }
            )
        return rows

    async def favorite_add(self, user_id: str, bvid: str, fid: int) -> dict[str, Any]:
        bundle = await self.load_credential(user_id)
        from bilibili_api import favorite_list, video

        info = await video.Video(bvid=bvid, credential=self._api_credential(bundle)).get_info()
        aid = int(info["aid"])
        try:
            await favorite_list.FavoriteListResource(
                aid, type_=2, credential=self._api_credential(bundle)
            ).add(fid)
            return {"ok": True, "bvid": bvid}
        except Exception as exc:  # noqa: BLE001
            message = str(exc)
            if "已经" in message or "already" in message.lower():
                return {"ok": True, "bvid": bvid, "already": True}
            raise BiliError(message[:300]) from exc

    async def send_comment(
        self,
        user_id: str,
        bvid: str,
        text: str,
        *,
        rpid: int = 0,
        root: int = 0,
    ) -> dict[str, Any]:
        bundle = await self.load_credential(user_id)
        from bilibili_api import comment, video

        info = await video.Video(bvid=bvid, credential=self._api_credential(bundle)).get_info()
        kwargs: dict[str, Any] = {
            "text": text,
            "oid": info["aid"],
            "type_": comment.CommentResourceType.VIDEO,
            "credential": self._api_credential(bundle),
        }
        if rpid:
            kwargs["root"] = root or rpid
            kwargs["parent"] = rpid
        result = await comment.send_comment(**kwargs)
        out: dict[str, Any] = {"success": True, "message": "评论发送成功"}
        if isinstance(result, dict):
            for key in ("rpid", "rpid_str", "root", "parent"):
                value = result.get(key)
                if value not in (None, "", 0):
                    out[key] = value
        return out

    async def list_dm_sessions(self, user_id: str) -> list[dict[str, Any]]:
        bundle = await self.load_credential(user_id)
        data = await _dm_get(DM_SESSIONS, bundle)
        rows = []
        for item in data.get("session_list") or []:
            preview = ""
            try:
                content = json.loads((item.get("last_msg") or {}).get("content") or "{}")
                preview = str(content.get("content") or content.get("text") or "")[:80]
            except Exception:  # noqa: BLE001
                pass
            uid = _int(item.get("talker_id"))
            if uid:
                rows.append(
                    {
                        "uid": uid,
                        "unread": _int(item.get("unread_count")),
                        "last_ts": _int((item.get("last_msg") or {}).get("timestamp")),
                        "preview": preview,
                    }
                )
        return rows

    async def fetch_dm_messages(self, user_id: str, talker_id: int, size: int = 10) -> list[dict[str, Any]]:
        bundle = await self.load_credential(user_id)
        data = await _dm_get(DM_MESSAGES.format(uid=int(talker_id), size=max(1, min(size, 50))), bundle)
        rows = []
        for item in reversed(data.get("messages") or []):
            try:
                content = json.loads(item.get("content") or "{}")
                text = str(content.get("content") or content.get("text") or "")
            except Exception:  # noqa: BLE001
                text = str(item.get("content") or "")
            rows.append(
                {
                    "key": str(item.get("msg_key") or item.get("msg_seqno") or item.get("timestamp")),
                    "ts": _int(item.get("timestamp")),
                    "from_me": _int(item.get("sender_uid")) == int(bundle.dedeuserid),
                    "text": (text or "[非文字消息]")[:500],
                }
            )
        return rows

    async def send_dm(self, user_id: str, receiver_uid: int, text: str) -> dict[str, Any]:
        bundle = await self.load_credential(user_id)
        from bilibili_api import session

        result = await session.send_msg(
            credential=self._api_credential(bundle),
            receiver_id=int(receiver_uid),
            msg_type=session.EventType.TEXT,
            content=text.strip(),
        )
        data = result if isinstance(result, dict) else {}
        code = data.get("code", 0)
        inner = data.get("data")
        msg_key = data.get("msg_key") or (inner.get("msg_key") if isinstance(inner, dict) else None)
        if code not in (0, None):
            raise BiliError(
                f"接口拒绝（code={code}）：{data.get('message') or data.get('msg') or '未知原因'}"
            )
        if not msg_key:
            raise BiliError("接口回了成功，但没有投递回执 msg_key")
        return {"success": True, "message": "私信已投递", "msg_key": str(msg_key)}

    async def transcribe(
        self,
        user_id: str,
        bvid: str,
        *,
        model: str = "small",
        work_dir: str = ".data/asr",
    ) -> dict[str, Any]:
        """Optional local ASR. Requires the asr extra and ffmpeg."""
        try:
            from faster_whisper import WhisperModel
        except ImportError as exc:
            raise BiliError("local ASR is not installed; install the `asr` extra") from exc
        from bilibili_api import video

        bundle = await self.load_credential(user_id)
        api_video = video.Video(bvid=bvid, credential=self._api_credential(bundle))
        info = await api_video.get_info()
        cid = info.get("cid") or ((info.get("pages") or [{}])[0].get("cid"))
        download = await api_video.get_download_url(cid=cid)
        audio_urls = (download.get("dash") or {}).get("audio") or []
        if not audio_urls:
            raise BiliError("no audio stream available")
        audio_url = audio_urls[0].get("baseUrl") or audio_urls[0].get("base_url")
        if not audio_url:
            raise BiliError("audio stream URL missing")
        timeout = aiohttp.ClientTimeout(total=180)
        import os
        from pathlib import Path

        target = Path(work_dir)
        target.mkdir(parents=True, exist_ok=True)
        audio_path = target / f"{bvid}.m4s"
        async with aiohttp.ClientSession(timeout=timeout) as client:
            async with client.get(audio_url, headers={"User-Agent": UA}) as response:
                response.raise_for_status()
                with audio_path.open("wb") as handle:
                    async for chunk in response.content.iter_chunked(1024 * 256):
                        handle.write(chunk)
        whisper = WhisperModel(model, device="cpu", compute_type="int8")

        def transcribe_file():
            segments, metadata = whisper.transcribe(str(audio_path), vad_filter=True)
            text = "\n".join(segment.text.strip() for segment in segments).strip()
            return text, metadata

        text, metadata = await asyncio.to_thread(transcribe_file)
        try:
            os.remove(audio_path)
        except OSError:
            pass
        return {
            "bvid": bvid,
            "language": getattr(metadata, "language", ""),
            "text": text,
            "empty": not text,
        }


async def _dm_get(url: str, bundle: CredentialBundle) -> dict:
    headers = {
        "User-Agent": UA,
        "Referer": "https://message.bilibili.com/",
        "Origin": "https://message.bilibili.com",
        "Cookie": (f"SESSDATA={bundle.sessdata}; bili_jct={bundle.bili_jct}; buvid3={bundle.buvid3}"),
    }
    timeout = aiohttp.ClientTimeout(total=25)
    async with aiohttp.ClientSession(timeout=timeout) as client:
        async with client.get(url, headers=headers) as response:
            payload = await response.json(content_type=None)
    if payload.get("code") != 0:
        raise BiliError(
            f"HTTP {response.status} code={payload.get('code')} msg={payload.get('message') or ''}"
        )
    return payload.get("data") or {}


def _timestamp(value: Any) -> datetime | None:
    try:
        stamp = int(value or 0)
        return datetime.fromtimestamp(stamp, UTC) if stamp else None
    except (TypeError, ValueError, OSError):
        return None


def _int(value: Any) -> int:
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def _subtitle_is_byline(text: str) -> bool:
    compact = "".join(text.split())
    return len(compact) < 16 and ("字幕" in compact or "subtitle" in compact.lower())


async def credential_exists(session: AsyncSession, user_id: str) -> bool:
    row = await session.scalar(select(BiliCredential.user_id).where(BiliCredential.user_id == user_id))
    return bool(row)
