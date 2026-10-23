from __future__ import annotations

from datetime import UTC, datetime
from urllib.parse import urlparse

import aiohttp
from fastapi import APIRouter, Depends, HTTPException, Query, Request, Response
from fastapi.responses import JSONResponse, StreamingResponse
from sqlalchemy import desc, select
from sqlalchemy import update as sql_update
from sqlalchemy.ext.asyncio import AsyncSession

from ..audit import record_audit
from ..crypto import new_secret
from ..deps import client_ip, get_session, require_csrf, web_identity
from ..models import (
    ApiToken,
    BiliCredential,
    Delivery,
    Report,
    Run,
    Schedule,
    User,
    VideoRecord,
    now_utc,
)
from ..schemas import MemberConfigPatch, TokenCreateRequest
from ..services.balance import BalanceError, refresh_deepseek_balance
from ..services.bilibili import (
    BilibiliService,
    login_broker,
    poll_login,
    start_login,
)
from ..services.config import load_admin_settings, load_config, save_config
from ..services.deliveries import verify_recent
from ..services.runners import runner_status
from ..services.runs import (
    RunError,
    activate_run,
    count_picked,
    create_run,
    issue_run_token,
    list_watched,
    local_day,
    today_report,
)
from ..services.usage import usage_dashboard
from ..timeutil import as_utc, next_daily_at

router = APIRouter(prefix="/me", tags=["member"])


async def _require_step_up(request: Request, session: AsyncSession, user_id: str) -> None:
    raw = request.headers.get("x-step-up-token", "")
    if not raw:
        raise HTTPException(status_code=403, detail="password and TOTP step-up required")
    token = await session.scalar(
        select(ApiToken).where(
            ApiToken.id_hash == request.app.state.cipher.token_hash(raw),
            ApiToken.user_id == user_id,
            ApiToken.kind == "stepup",
            ApiToken.revoked_at.is_(None),
        )
    )
    if not token or (token.expires_at and as_utc(token.expires_at) <= now_utc()):
        raise HTTPException(status_code=403, detail="step-up token is invalid or expired")
    if "outbound:enable" not in (token.scopes or []):
        raise HTTPException(status_code=403, detail="step-up token lacks permission")
    token.revoked_at = now_utc()


@router.get("/status")
async def status(
    request: Request,
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    config = await load_config(session, identity.user.id)
    schedule = await session.get(Schedule, identity.user.id)
    credential = await session.get(BiliCredential, identity.user.id)
    admin = await load_admin_settings(session)
    picked = await count_picked(session, identity.user.id, local_day(config))
    active = await session.scalar(
        select(Run)
        .where(Run.user_id == identity.user.id, Run.status.in_(["queued", "running"]))
        .order_by(desc(Run.scheduled_for))
    )
    latest = await session.scalar(
        select(Run).where(Run.user_id == identity.user.id).order_by(desc(Run.scheduled_for))
    )
    return {
        "ok": True,
        "user": {
            "id": identity.user.id,
            "username": identity.user.username,
            "display_name": identity.user.display_name,
            "role": identity.user.role,
        },
        "config": config,
        "schedule": {
            "enabled": bool(schedule and schedule.enabled),
            "time": schedule.local_time if schedule else config["schedule"]["time"],
            "timezone": schedule.timezone if schedule else config["schedule"]["timezone"],
            "next_run_at": schedule.next_run_at.isoformat() if schedule and schedule.next_run_at else None,
        },
        "bilibili": {
            "logged_in": bool(credential),
            "uid": credential.uid if credential else None,
            "nickname": credential.nickname if credential else None,
        },
        "picked_today": picked,
        "remaining_today": max(0, int(config.get("perDay") or 0) - picked),
        "runners": runner_status(request.app.state.settings),
        "active_run": _run_dict(active) if active else None,
        "latest_run": _run_dict(latest) if latest else None,
        "admin_settings": {
            "outbound_enabled": bool(admin.get("outbound_enabled", True)),
        },
        "version": request.app.state.version,
    }


@router.get("/config")
async def get_config(
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    return {"ok": True, "config": await load_config(session, identity.user.id)}


@router.patch("/config")
async def patch_config(
    body: MemberConfigPatch,
    request: Request,
    identity=Depends(require_csrf),
    session: AsyncSession = Depends(get_session),
) -> dict:
    raw = body.model_dump(exclude_none=True)
    outbound_change = bool(
        raw.get("autoComment")
        or raw.get("replyToComments")
        or raw.get("dmWatch")
        or (raw.get("dmMode") in {"only", "both"})
        or int(raw.get("favFolderId") or 0) > 0
    )
    if outbound_change:
        await _require_step_up(request, session, identity.user.id)
    try:
        config = await save_config(session, identity.user.id, body)
    except ValueError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    schedule = await session.get(Schedule, identity.user.id)
    if schedule:
        schedule.next_run_at = (
            next_daily_at(schedule.local_time, schedule.timezone) if schedule.enabled else None
        )
    await record_audit(
        session,
        event="member.config_update",
        user_id=identity.user.id,
        actor=identity.user,
        detail={"keys": sorted(raw)},
        ip=client_ip(request),
    )
    return {"ok": True, "config": config}


@router.post("/runs")
async def run_now(
    request: Request,
    identity=Depends(require_csrf),
    session: AsyncSession = Depends(get_session),
) -> dict:
    runners = runner_status(request.app.state.settings)
    config = await load_config(session, identity.user.id)
    chosen = str(config.get("runner") or "codex")
    if not runners.get(chosen, {}).get("available"):
        raise HTTPException(status_code=409, detail=f"{chosen} runner is not installed")
    try:
        run = await create_run(
            session,
            request.app.state.cipher,
            request.app.state.settings,
            user=identity.user,
            kind="manual",
            runner=chosen,
        )
        token = await issue_run_token(
            session,
            request.app.state.cipher,
            request.app.state.settings,
            user=identity.user,
            run=run,
        )
        await activate_run(session, run)
    except RunError as exc:
        raise HTTPException(status_code=409, detail=str(exc)) from exc
    run_id, runner = run.id, run.runner
    request.app.state.scheduler.submit_run(
        run_id=run_id,
        user_id=identity.user.id,
        runner_name=runner,
        token=token,
    )
    await request.app.state.events.publish(identity.user.id, "run", {"run_id": run_id, "status": "running"})
    return {"ok": True, "run_id": run_id, "status": "running", "runner": runner}


@router.get("/runs")
async def list_runs(
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
    limit: int = Query(default=20, ge=1, le=100),
) -> dict:
    runs = (
        await session.scalars(
            select(Run).where(Run.user_id == identity.user.id).order_by(desc(Run.scheduled_for)).limit(limit)
        )
    ).all()
    return {"ok": True, "runs": [_run_dict(run) for run in runs]}


@router.post("/runs/{run_id}/cancel")
async def cancel_run(
    run_id: str,
    identity=Depends(require_csrf),
    session: AsyncSession = Depends(get_session),
) -> dict:
    run = await session.get(Run, run_id)
    if not run or run.user_id != identity.user.id:
        raise HTTPException(status_code=404, detail="run not found")
    if run.status not in {"queued", "running"}:
        return {"ok": True, "run_id": run.id, "status": run.status}
    run.status = "cancelled"
    run.finished_at = now_utc()
    await session.execute(
        sql_update(ApiToken)
        .where(ApiToken.run_id == run.id)
        .values(revoked_at=now_utc())
    )
    return {"ok": True, "run_id": run.id, "status": "cancelled"}


@router.get("/watched")
async def watched(
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
    state: str = "",
    tier: str = "",
    offset: int = Query(default=0, ge=0),
    limit: int = Query(default=30, ge=1, le=100),
) -> dict:
    return {
        "ok": True,
        **await list_watched(
            session,
            identity.user.id,
            state=state,
            tier=tier,
            offset=offset,
            limit=limit,
        ),
    }


@router.get("/report")
async def report(
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    return {"ok": True, **await today_report(session, identity.user.id)}


@router.get("/deliveries")
async def deliveries(
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    rows = (
        await session.scalars(
            select(Delivery)
            .where(Delivery.user_id == identity.user.id)
            .order_by(desc(Delivery.created_at))
            .limit(100)
        )
    ).all()
    return {
        "ok": True,
        "rows": [
            {
                "id": row.id,
                "kind": row.kind,
                "bvid": row.bvid,
                "content": row.content,
                "status": row.status,
                "remote_id": row.remote_id,
                "visible": row.visible,
                "created_at": row.created_at,
            }
            for row in rows
        ],
    }


@router.get("/usage")
async def usage(
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
    days: int = Query(default=30, ge=1, le=365),
) -> dict:
    return {"ok": True, **await usage_dashboard(session, identity.user.id, days)}


@router.post("/balance/refresh")
async def refresh_balance(
    request: Request,
    identity=Depends(require_csrf),
    session: AsyncSession = Depends(get_session),
) -> dict:
    try:
        result = await refresh_deepseek_balance(session, request.app.state.cipher, user_id=identity.user.id)
        return {"ok": True, **result}
    except BalanceError as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc


@router.post("/verify")
async def verify(
    request: Request,
    identity=Depends(require_csrf),
    session: AsyncSession = Depends(get_session),
) -> dict:
    items = await verify_recent(session, request.app.state.cipher, user_id=identity.user.id)
    return {"ok": True, "checked": items}


@router.post("/login/start")
async def login_start(
    identity=Depends(require_csrf),
) -> dict:
    return {"ok": True, **await start_login(identity.user.id)}


@router.post("/login/poll")
async def login_poll(
    request: Request,
    body: dict,
    identity=Depends(require_csrf),
    session: AsyncSession = Depends(get_session),
) -> dict:
    key = str(body.get("key") or "")
    if not key:
        raise HTTPException(status_code=422, detail="key is required")
    if await login_broker.owner(key) != identity.user.id:
        raise HTTPException(status_code=403, detail="login session belongs to another user")
    result = await poll_login(key)
    if result.get("state") == "ok":
        secret = result.pop("credential")
        bili = BilibiliService(session, request.app.state.cipher)
        credential = await bili.save_credential(identity.user.id, secret)
        config = await load_config(session, identity.user.id)
        if int(config.get("dmUid") or 0) <= 0 and credential.uid:
            await save_config(session, identity.user.id, MemberConfigPatch(dmUid=credential.uid))
        try:
            profile = await bili.whoami(identity.user.id)
            credential.nickname = str(profile.get("nickname") or "")
            credential.uid = int(profile.get("uid") or credential.uid or 0)
        except Exception:  # noqa: BLE001
            pass
        return {"ok": True, **result, "uid": credential.uid}
    return {"ok": result.get("state") not in {"unknown", "expired"}, **result}


@router.get("/fav-folders")
async def favorite_folders(
    request: Request,
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    try:
        folders = await BilibiliService(session, request.app.state.cipher).favorite_folders(identity.user.id)
        return {"ok": True, "folders": folders}
    except Exception as exc:  # noqa: BLE001
        return {"ok": False, "error": str(exc)[:300], "folders": []}


@router.post("/fav-today")
async def favorite_today(
    request: Request,
    identity=Depends(require_csrf),
    session: AsyncSession = Depends(get_session),
) -> dict:
    config = await load_config(session, identity.user.id)
    fid = int(config.get("favFolderId") or 0)
    if fid <= 0:
        raise HTTPException(status_code=422, detail="favorite folder is not configured")
    day = local_day(config)
    rows = (
        await session.scalars(
            select(VideoRecord).where(
                VideoRecord.user_id == identity.user.id,
                VideoRecord.day == day,
                VideoRecord.state == "picked",
            )
        )
    ).all()
    bili = BilibiliService(session, request.app.state.cipher)
    failures = []
    added = 0
    for row in rows:
        try:
            await bili.favorite_add(identity.user.id, row.bvid, fid)
            added += 1
        except Exception as exc:  # noqa: BLE001
            failures.append({"bvid": row.bvid, "error": str(exc)[:300]})
    return {"ok": not failures, "added": added, "total": len(rows), "failures": failures}


@router.get("/tokens")
async def list_tokens(
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    rows = (
        await session.scalars(
            select(ApiToken)
            .where(
                ApiToken.user_id == identity.user.id,
                ApiToken.kind == "device",
                ApiToken.revoked_at.is_(None),
            )
            .order_by(desc(ApiToken.created_at))
        )
    ).all()
    return {
        "ok": True,
        "tokens": [
            {
                "id_hash": row.id_hash[:12],
                "label": row.label,
                "created_at": row.created_at,
                "last_used_at": row.last_used_at,
            }
            for row in rows
        ],
    }


@router.post("/tokens")
async def create_token(
    body: TokenCreateRequest,
    request: Request,
    identity=Depends(require_csrf),
    session: AsyncSession = Depends(get_session),
) -> dict:
    raw = new_secret("bili_device", 32)
    scopes = [
        "bili:read",
        "bili:config",
        "bili:login",
        "bili:write",
        "bili:asr",
        "run:begin",
        "run:finish",
        "run:request",
        "usage:import",
    ]
    session.add(
        ApiToken(
            id_hash=request.app.state.cipher.token_hash(raw),
            user_id=identity.user.id,
            label=body.label,
            kind="device",
            scopes=scopes,
        )
    )
    return {"ok": True, "token": raw, "label": body.label}


@router.delete("/tokens/{prefix}")
async def revoke_token(
    prefix: str,
    identity=Depends(require_csrf),
    session: AsyncSession = Depends(get_session),
) -> dict:
    if len(prefix) < 8:
        raise HTTPException(status_code=422, detail="token prefix too short")
    rows = (
        await session.scalars(
            select(ApiToken).where(
                ApiToken.user_id == identity.user.id,
                ApiToken.kind == "device",
                ApiToken.revoked_at.is_(None),
            )
        )
    ).all()
    hit = next((row for row in rows if row.id_hash.startswith(prefix)), None)
    if not hit:
        raise HTTPException(status_code=404, detail="token not found")
    hit.revoked_at = now_utc()
    return {"ok": True}


@router.get("/diagnostics")
async def diagnostics(
    request: Request,
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> dict:
    config = await load_config(session, identity.user.id)
    credential = await session.get(BiliCredential, identity.user.id)
    return {
        "ok": True,
        "version": request.app.state.version,
        "user_id": identity.user.id,
        "runner": config.get("runner"),
        "runners": runner_status(request.app.state.settings),
        "bilibili_logged_in": bool(credential),
        "outbound": {
            "comment": bool(config.get("autoComment")),
            "dm_mode": config.get("dmMode"),
            "dm_watch": bool(config.get("dmWatch")),
        },
        "note": "Secrets and Bilibili cookies are intentionally excluded.",
    }


@router.get("/export")
async def export_data(
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> JSONResponse:
    config = await load_config(session, identity.user.id)
    reports = (
        await session.scalars(select(Report).where(Report.user_id == identity.user.id).order_by(Report.day))
    ).all()
    videos = (await session.scalars(select(VideoRecord).where(VideoRecord.user_id == identity.user.id))).all()
    deliveries = (await session.scalars(select(Delivery).where(Delivery.user_id == identity.user.id))).all()
    payload = {
        "exported_at": datetime.now(UTC).isoformat(),
        "user": _public_user(identity.user),
        "config": config,
        "reports": [
            {"day": item.day, "markdown": item.markdown, "summary": item.summary} for item in reports
        ],
        "videos": [
            {
                "bvid": item.bvid,
                "day": item.day,
                "title": item.title,
                "author": item.author,
                "url": item.url,
                "state": item.state,
                "tier": item.tier,
                "reason": item.reason,
            }
            for item in videos
        ],
        "deliveries": [
            {
                "kind": item.kind,
                "bvid": item.bvid,
                "content": item.content,
                "status": item.status,
                "created_at": item.created_at.isoformat(),
            }
            for item in deliveries
        ],
    }
    return JSONResponse(
        payload,
        headers={
            "Content-Disposition": (f'attachment; filename="bili-scroll-{identity.user.username}.json"')
        },
    )


@router.get("/cover")
async def cover(
    bvid: str,
    identity=Depends(web_identity),
    session: AsyncSession = Depends(get_session),
) -> Response:
    row = await session.scalar(
        select(VideoRecord).where(VideoRecord.user_id == identity.user.id, VideoRecord.bvid == bvid)
    )
    source = str((row.metadata_json or {}).get("cover") or "") if row else ""
    if not source:
        return Response(
            content=_placeholder_svg(),
            media_type="image/svg+xml",
            headers={"Cache-Control": "public, max-age=3600"},
        )
    parsed = urlparse(source)
    if parsed.scheme not in {"http", "https"} or not (parsed.hostname or "").endswith(".hdslb.com"):
        raise HTTPException(status_code=400, detail="unsupported cover URL")
    timeout = aiohttp.ClientTimeout(total=20)
    async with aiohttp.ClientSession(timeout=timeout) as client, client.get(source) as response:
        if response.status != 200:
            raise HTTPException(status_code=502, detail="cover download failed")
        payload = await response.read()
        content_type = response.headers.get("content-type", "image/jpeg")
    return Response(payload, media_type=content_type, headers={"Cache-Control": "private, max-age=86400"})


@router.get("/events")
async def events(
    request: Request,
    identity=Depends(web_identity),
) -> StreamingResponse:
    return StreamingResponse(
        request.app.state.events.stream(identity.user.id),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


def _run_dict(run: Run) -> dict:
    return {
        "id": run.id,
        "kind": run.kind,
        "runner": run.runner,
        "status": run.status,
        "scheduled_for": run.scheduled_for,
        "started_at": run.started_at,
        "finished_at": run.finished_at,
        "picked_count": run.picked_count,
        "error": run.error,
    }


def _public_user(user: User) -> dict:
    return {
        "id": user.id,
        "username": user.username,
        "display_name": user.display_name,
        "role": user.role,
    }


def _placeholder_svg() -> bytes:
    return (
        b'<svg xmlns="http://www.w3.org/2000/svg" width="640" height="360" '
        b'viewBox="0 0 640 360"><rect width="100%" height="100%" fill="#15171c"/>'
        b'<path d="M260 140h120v80H260z" fill="#2a2e36"/>'
        b'<text x="320" y="250" text-anchor="middle" fill="#7d8696" '
        b'font-family="sans-serif" font-size="20">Bili Scroll</text></svg>'
    )
