from __future__ import annotations

import re
from datetime import UTC, datetime, timedelta
from zoneinfo import ZoneInfo

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..config import Settings
from ..crypto import SecretCipher, new_secret
from ..models import (
    ApiToken,
    BiliCredential,
    Report,
    Run,
    RunCandidate,
    Schedule,
    User,
    VideoRecord,
    now_utc,
)
from ..schemas import PickedItem, RunSummary, SkippedItem
from .config import load_admin_settings, load_config
from .deliveries import process_deliveries

BVID_RE = re.compile(r"^BV[0-9A-Za-z]{10}$")


class RunError(ValueError):
    pass


def local_day(config: dict, now: datetime | None = None) -> str:
    timezone = (config.get("schedule") or {}).get("timezone") or "Asia/Shanghai"
    value = (now or datetime.now(UTC)).astimezone(ZoneInfo(timezone))
    return value.date().isoformat()


async def create_run(
    session: AsyncSession,
    cipher: SecretCipher,
    settings: Settings,
    *,
    user: User,
    kind: str,
    runner: str | None = None,
    scheduled_for: datetime | None = None,
) -> Run:
    config = await load_config(session, user.id)
    admin = await load_admin_settings(session)
    if await _active_run(session, user.id):
        raise RunError("a run is already active for this user")
    per_day_key = datetime.now(UTC).strftime("%Y-%m-%d")
    count = int(
        await session.scalar(
            select(func.count())
            .select_from(Run)
            .where(
                Run.user_id == user.id,
                Run.kind == kind,
                func.strftime("%Y-%m-%d", Run.scheduled_for) == per_day_key,
            )
        )
        or 0
    )
    limit = (
        int(admin.get("manual_runs_per_day", settings.manual_runs_per_day))
        if kind == "manual"
        else int(admin.get("scheduled_runs_per_day", settings.scheduled_runs_per_day))
    )
    if kind != "dm" and (limit <= 0 or count >= limit):
        raise RunError(f"daily {kind} run limit reached")
    if not await session.get(BiliCredential, user.id):
        raise RunError("Bilibili login is required")
    chosen_runner = runner or str(config.get("runner") or admin.get("default_runner") or "codex")
    if chosen_runner not in {"codex", "hermes"}:
        raise RunError("unknown runner")
    row = Run(
        user_id=user.id,
        kind=kind,
        runner=chosen_runner,
        status="queued",
        scheduled_for=scheduled_for or now_utc(),
    )
    session.add(row)
    await session.flush()
    return row


async def issue_run_token(
    session: AsyncSession,
    cipher: SecretCipher,
    settings: Settings,
    *,
    user: User,
    run: Run,
    scopes: list[str] | None = None,
) -> str:
    token = new_secret("bili_run", 32)
    session.add(
        ApiToken(
            id_hash=cipher.token_hash(token),
            user_id=user.id,
            label=f"run:{run.id}",
            kind="run",
            scopes=scopes
            or [
                "run:begin",
                "bili:read",
                "run:finish",
                "bili:asr",
                "bili:reply",
            ],
            run_id=run.id,
            expires_at=now_utc() + timedelta(minutes=settings.run_token_minutes),
        )
    )
    return token


async def activate_run(session: AsyncSession, run: Run) -> Run:
    if run.status not in {"queued", "running"}:
        raise RunError("run cannot be started")
    if run.status == "queued":
        run.status = "running"
        run.started_at = now_utc()
    run.heartbeat_at = now_utc()
    return run


async def record_candidates(
    session: AsyncSession,
    *,
    run_id: str,
    user_id: str,
    videos: list[dict],
) -> list[dict]:
    existing_ids = set(
        (await session.scalars(select(VideoRecord.bvid).where(VideoRecord.user_id == user_id))).all()
    )
    accepted: list[dict] = []
    for video in videos:
        bvid = str(video.get("bvid") or "")
        if not BVID_RE.fullmatch(bvid) or bvid in existing_ids:
            continue
        row = await session.scalar(
            select(RunCandidate).where(RunCandidate.run_id == run_id, RunCandidate.bvid == bvid)
        )
        if row:
            row.payload = video
        else:
            session.add(
                RunCandidate(
                    run_id=run_id,
                    user_id=user_id,
                    bvid=bvid,
                    payload=video,
                )
            )
        accepted.append(video)
    return accepted


async def finish_run(
    session: AsyncSession,
    cipher: SecretCipher,
    settings: Settings,
    *,
    user: User,
    run: Run,
    summary: RunSummary,
) -> dict:
    if run.user_id != user.id or run.status != "running":
        raise RunError("run is not active")
    config = await load_config(session, user.id)
    day = local_day(config)
    candidates = {
        row.bvid: row
        for row in (await session.scalars(select(RunCandidate).where(RunCandidate.run_id == run.id))).all()
    }
    picked_ids = {item.bvid for item in summary.picked}
    skipped_ids = {item.bvid for item in summary.skipped}
    unknown = (picked_ids | skipped_ids) - set(candidates)
    if unknown:
        raise RunError(f"summary contains BVIDs outside this run: {', '.join(sorted(unknown))}")
    if picked_ids & skipped_ids:
        raise RunError("a video cannot be both picked and skipped")

    already = await count_picked(session, user.id, day)
    quota = int(config.get("perDay") or 0)
    remaining = max(0, quota - already)
    picked = summary.picked[:remaining]
    dropped = summary.picked[remaining:]
    skipped = list(summary.skipped) + [
        SkippedItem(
            bvid=item.bvid,
            reason="daily quota reached",
            title=item.title,
        )
        for item in dropped
    ]

    for item in picked:
        candidate = candidates[item.bvid]
        payload = candidate.payload or {}
        await _upsert_video(
            session,
            user_id=user.id,
            run_id=run.id,
            day=day,
            bvid=item.bvid,
            title=item.title or str(payload.get("title") or ""),
            author=item.author or str(payload.get("author") or ""),
            url=item.url or str(payload.get("url") or f"https://www.bilibili.com/video/{item.bvid}"),
            state="picked",
            tier=item.tier,
            reason=item.reason,
            one_liner=item.one_liner,
            payload=payload,
        )
    for item in skipped:
        candidate = candidates[item.bvid]
        payload = candidate.payload or {}
        await _upsert_video(
            session,
            user_id=user.id,
            run_id=run.id,
            day=day,
            bvid=item.bvid,
            title=item.title or str(payload.get("title") or ""),
            author=str(payload.get("author") or ""),
            url=str(payload.get("url") or f"https://www.bilibili.com/video/{item.bvid}"),
            state="skipped",
            tier=None,
            reason=item.reason,
            one_liner=None,
            payload=payload,
        )

    report_text = _render_report(
        day=day,
        picked=picked,
        summary=summary,
        report_level=str(config.get("reportLevel") or "full"),
    )
    report = await session.scalar(select(Report).where(Report.user_id == user.id, Report.day == day))
    if not report:
        report = Report(user_id=user.id, day=day, markdown=report_text, summary=summary.summary)
        session.add(report)
    elif report_text:
        report.markdown = (report.markdown.rstrip() + "\n\n" + report_text).strip()
        report.summary = summary.summary
        report.updated_at = now_utc()

    await _write_export_files(
        settings=settings,
        user_id=user.id,
        day=day,
        report_text=report_text if config.get("reportLevel") != "off" else "",
        picked=picked,
        skipped=skipped,
        summary=summary.summary,
    )
    deliveries = await process_deliveries(
        session,
        cipher,
        settings,
        user=user,
        run=run,
        config=config,
        picked=picked,
        delivery_drafts=summary.delivery_drafts,
    )
    run.status = "succeeded"
    run.finished_at = now_utc()
    run.picked_count = len(picked)
    run.telemetry = {
        "runner": run.runner,
        "picked": len(picked),
        "skipped": len(skipped),
        "no_subtitle_count": summary.no_subtitle_count,
        "deliveries": deliveries,
    }
    schedule = await session.get(Schedule, user.id)
    if schedule:
        schedule.last_run_at = run.finished_at
    return {
        "ok": True,
        "run_id": run.id,
        "picked": len(picked),
        "remaining_today": max(0, quota - already - len(picked)),
        "dropped_for_quota": [item.bvid for item in dropped],
        "deliveries": deliveries,
    }


async def fail_run(session: AsyncSession, run: Run, message: str) -> None:
    run.status = "failed"
    run.error = message[:4000]
    run.finished_at = now_utc()


async def count_picked(session: AsyncSession, user_id: str, day: str) -> int:
    return int(
        await session.scalar(
            select(func.count())
            .select_from(VideoRecord)
            .where(
                VideoRecord.user_id == user_id,
                VideoRecord.day == day,
                VideoRecord.state == "picked",
            )
        )
        or 0
    )


async def list_watched(
    session: AsyncSession,
    user_id: str,
    *,
    state: str = "",
    tier: str = "",
    offset: int = 0,
    limit: int = 30,
) -> dict:
    clauses = [VideoRecord.user_id == user_id]
    if state:
        clauses.append(VideoRecord.state == state)
    if tier:
        clauses.append(VideoRecord.tier == tier)
    total = int(await session.scalar(select(func.count()).select_from(VideoRecord).where(*clauses)) or 0)
    rows = (
        await session.scalars(
            select(VideoRecord)
            .where(*clauses)
            .order_by(VideoRecord.seen_at.desc())
            .offset(max(0, offset))
            .limit(max(1, min(limit, 100)))
        )
    ).all()
    return {
        "total": total,
        "offset": offset,
        "limit": limit,
        "rows": [
            {
                "bvid": row.bvid,
                "day": row.day,
                "title": row.title,
                "up": row.author,
                "url": row.url,
                "picked": row.state == "picked",
                "state": row.state,
                "tier": row.tier or "",
                "why": row.reason or "",
                "one_liner": row.one_liner or "",
                "delivered": row.delivered,
                "cover": (f"/api/me/cover?bvid={row.bvid}" if row.metadata_json.get("cover") else ""),
            }
            for row in rows
        ],
    }


async def today_report(session: AsyncSession, user_id: str) -> dict:
    config = await load_config(session, user_id)
    day = local_day(config)
    row = await session.scalar(select(Report).where(Report.user_id == user_id, Report.day == day))
    return {
        "day": day,
        "exists": bool(row and row.markdown),
        "markdown": row.markdown if row else "",
        "summary": row.summary if row else "",
    }


async def _active_run(session: AsyncSession, user_id: str) -> Run | None:
    return await session.scalar(
        select(Run).where(
            Run.user_id == user_id,
            Run.status.in_(["queued", "running"]),
        )
    )


async def _upsert_video(
    session: AsyncSession,
    *,
    user_id: str,
    run_id: str,
    day: str,
    bvid: str,
    title: str,
    author: str,
    url: str,
    state: str,
    tier: str | None,
    reason: str | None,
    one_liner: str | None,
    payload: dict,
) -> VideoRecord:
    row = await session.scalar(
        select(VideoRecord).where(VideoRecord.user_id == user_id, VideoRecord.bvid == bvid)
    )
    if not row:
        row = VideoRecord(
            user_id=user_id,
            bvid=bvid,
            first_run_id=run_id,
            day=day,
        )
        session.add(row)
    row.title = title or row.title
    row.author = author or row.author
    row.url = url or row.url
    row.state = state
    row.tier = tier
    row.reason = reason
    row.one_liner = one_liner
    row.metadata_json = {
        "play": payload.get("play", 0),
        "review": payload.get("review", 0),
        "pubdate": payload.get("pubdate"),
        "cover": payload.get("cover"),
    }
    return row


def _render_report(
    *,
    day: str,
    picked: list[PickedItem],
    summary: RunSummary,
    report_level: str,
) -> str:
    if report_level == "off":
        return ""
    lines = [f"# 刷 B 站日報 {day}", ""]
    for item in picked:
        if report_level == "brief":
            lines.extend(
                [
                    f"### {item.bvid}",
                    f"- {item.tier} · {item.url or 'https://www.bilibili.com/video/' + item.bvid}",
                    "",
                ]
            )
            continue
        lines.extend(
            [
                f"### {item.bvid}",
                f"- **標題**：{item.title or '未提供'}",
                f"- **UP 主**：{item.author or '未提供'}",
                f"- **連結**：{item.url or 'https://www.bilibili.com/video/' + item.bvid}",
                f"- **檔位**：{item.tier}",
                f"- **為什麼值得**：{item.reason}",
            ]
        )
        if item.one_liner:
            lines.append(f"- **一句話**：{item.one_liner}")
        lines.append("")
    lines.extend(
        [
            f"> 因無字幕跳過 {summary.no_subtitle_count} 條。",
            "",
        ]
    )
    if summary.summary:
        lines.extend([summary.summary.strip(), ""])
    return "\n".join(lines).strip() + "\n"


async def _write_export_files(
    *,
    settings: Settings,
    user_id: str,
    day: str,
    report_text: str,
    picked: list[PickedItem],
    skipped: list[SkippedItem],
    summary: str,
) -> None:
    user_dir = settings.data_dir / "users" / user_id
    reports = user_dir / "reports"
    digest = user_dir / "digest"
    reports.mkdir(parents=True, exist_ok=True)
    digest.mkdir(parents=True, exist_ok=True)
    if report_text:
        existing = reports / f"{day}.md"
        with existing.open("a", encoding="utf-8") as handle:
            if existing.stat().st_size:
                handle.write("\n\n")
            handle.write(report_text)
    archive = digest / f"{day[:7]}.md"
    with archive.open("a", encoding="utf-8") as handle:
        for item in picked:
            handle.write(
                f"{day} · {item.bvid} · {item.title} · {item.author} · 挑中（{item.tier}）· {item.url}\n"
            )
        for item in skipped:
            handle.write(f"{day} · {item.bvid} · {item.title} · 跳過（{item.reason}）\n")
        if summary:
            handle.write(f"{day} · 回合總結：{summary}\n")
