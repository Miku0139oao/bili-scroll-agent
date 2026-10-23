from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator


class ApiModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class LoginRequest(ApiModel):
    username: str = Field(min_length=1, max_length=64)
    password: str = Field(min_length=1, max_length=256)
    totp: str = Field(min_length=6, max_length=12)


class InviteAcceptRequest(ApiModel):
    token: str = Field(min_length=20, max_length=200)
    password: str = Field(min_length=12, max_length=256)


class InviteVerifyRequest(ApiModel):
    setup_token: str = Field(min_length=20, max_length=300)
    totp: str = Field(min_length=6, max_length=12)


class InviteCreateRequest(ApiModel):
    username: str = Field(pattern=r"^[a-z0-9][a-z0-9._-]{1,63}$")
    display_name: str = Field(min_length=1, max_length=100)
    role: Literal["member", "admin"] = "member"


class StepUpRequest(ApiModel):
    password: str = Field(min_length=1, max_length=256)
    totp: str = Field(min_length=6, max_length=12)


class ScheduleConfig(ApiModel):
    enabled: bool = False
    time: str = "20:00"
    timezone: str = "Asia/Shanghai"

    @field_validator("time")
    @classmethod
    def valid_time(cls, value: str) -> str:
        try:
            hour, minute = value.split(":", 1)
            if not (0 <= int(hour) <= 23 and 0 <= int(minute) <= 59):
                raise ValueError
        except (ValueError, AttributeError) as exc:
            raise ValueError("time must use HH:MM") from exc
        return value


class MemberConfig(ApiModel):
    runner: Literal["codex", "hermes"] = "codex"
    keywords: list[str] = Field(default_factory=list, max_length=30)
    persona: str = Field(default="", max_length=8000)
    perDay: int = Field(default=5, ge=0, le=50)
    reportLevel: Literal["full", "brief", "off"] = "full"
    language: Literal["zh-TW", "zh-CN", "en"] = "zh-TW"
    autoComment: bool = False
    commentPerRound: int = Field(default=1, ge=0, le=20)
    mentionName: str = Field(default="", max_length=100)
    dmMode: Literal["off", "only", "both"] = "off"
    dmUid: int = Field(default=0, ge=0)
    replyToComments: bool = False
    replyPerRound: int = Field(default=1, ge=0, le=20)
    dmWatch: bool = False
    dmWatchMinutes: int = Field(default=5, ge=3, le=1440)
    dmReplyPerDay: int = Field(default=0, ge=0, le=200)
    favFolderId: int = Field(default=0, ge=0)
    transcribe: bool = False
    asrModel: Literal["tiny", "base", "small", "medium"] = "small"
    schedule: ScheduleConfig = Field(default_factory=ScheduleConfig)


class MemberConfigPatch(ApiModel):
    runner: Literal["codex", "hermes"] | None = None
    keywords: list[str] | None = Field(default=None, max_length=30)
    persona: str | None = Field(default=None, max_length=8000)
    perDay: int | None = Field(default=None, ge=0, le=50)
    reportLevel: Literal["full", "brief", "off"] | None = None
    language: Literal["zh-TW", "zh-CN", "en"] | None = None
    autoComment: bool | None = None
    commentPerRound: int | None = Field(default=None, ge=0, le=20)
    mentionName: str | None = Field(default=None, max_length=100)
    dmMode: Literal["off", "only", "both"] | None = None
    dmUid: int | None = Field(default=None, ge=0)
    replyToComments: bool | None = None
    replyPerRound: int | None = Field(default=None, ge=0, le=20)
    dmWatch: bool | None = None
    dmWatchMinutes: int | None = Field(default=None, ge=3, le=1440)
    dmReplyPerDay: int | None = Field(default=None, ge=0, le=200)
    favFolderId: int | None = Field(default=None, ge=0)
    transcribe: bool | None = None
    asrModel: Literal["tiny", "base", "small", "medium"] | None = None
    schedule: ScheduleConfig | None = None


class PickedItem(ApiModel):
    bvid: str = Field(pattern=r"^BV[0-9A-Za-z]{10}$")
    title: str = Field(default="", max_length=500)
    author: str = Field(default="", max_length=200)
    url: str = Field(default="", max_length=500)
    tier: Literal["值得看", "可以看", "不值当"]
    reason: str = Field(min_length=1, max_length=1200)
    one_liner: str = Field(default="", max_length=500)
    comment_text: str = Field(default="", max_length=150)


class SkippedItem(ApiModel):
    bvid: str = Field(pattern=r"^BV[0-9A-Za-z]{10}$")
    reason: str = Field(min_length=1, max_length=300)
    title: str = Field(default="", max_length=500)


class DeliveryDraft(ApiModel):
    kind: Literal["comment", "dm"]
    bvid: str = Field(default="", max_length=20)
    text: str = Field(min_length=1, max_length=2000)


class RunSummary(ApiModel):
    run_id: str
    picked: list[PickedItem] = Field(default_factory=list, max_length=50)
    skipped: list[SkippedItem] = Field(default_factory=list, max_length=200)
    summary: str = Field(default="", max_length=3000)
    no_subtitle_count: int = Field(default=0, ge=0, le=500)
    delivery_drafts: list[DeliveryDraft] = Field(default_factory=list, max_length=50)
    completed_at: datetime | None = None


class DeliveryRequest(ApiModel):
    kind: Literal["comment", "dm"]
    bvid: str = Field(default="", max_length=20)
    text: str = Field(min_length=1, max_length=2000)
    confirm: bool = False


class TokenCreateRequest(ApiModel):
    label: str = Field(min_length=1, max_length=100)


class AdminRunnerSettings(ApiModel):
    default_runner: Literal["codex", "hermes"] = "codex"
    codex_provider: str = Field(default="", max_length=120)
    codex_model: str = Field(default="", max_length=160)
    codex_reasoning: str = Field(default="", max_length=40)
    hermes_provider: str = Field(default="", max_length=120)
    hermes_model: str = Field(default="", max_length=160)
    outbound_enabled: bool = True
    global_concurrency: int = Field(default=1, ge=1, le=8)
    manual_runs_per_day: int = Field(default=3, ge=0, le=50)
    scheduled_runs_per_day: int = Field(default=1, ge=0, le=12)
    retention_days: int = Field(default=365, ge=1, le=3650)


class AdminUserPatch(ApiModel):
    disabled: bool | None = None
    role: Literal["admin", "member"] | None = None


class UsageRow(ApiModel):
    date: str = Field(pattern=r"^\d{4}-\d{2}-\d{2}$")
    app_type: Literal["codex", "hermes"] = "codex"
    provider_id: str = Field(default="", max_length=120)
    model: str = Field(default="", max_length=160)
    request_count: int = Field(default=0, ge=0)
    success_count: int = Field(default=0, ge=0)
    input_tokens: int = Field(default=0, ge=0)
    output_tokens: int = Field(default=0, ge=0)
    cache_read_tokens: int = Field(default=0, ge=0)
    cache_creation_tokens: int = Field(default=0, ge=0)
    total_cost_usd: float = Field(default=0, ge=0)
    avg_latency_ms: float = Field(default=0, ge=0)


class UsageImportRequest(ApiModel):
    source: str = Field(default="cc-switch", pattern=r"^[a-z0-9._-]{1,40}$")
    rows: list[UsageRow] = Field(default_factory=list, max_length=10000)


class BalanceInfo(ApiModel):
    currency: str = Field(default="CNY", max_length=12)
    total: float = Field(default=0, ge=0)
    granted: float = Field(default=0, ge=0)
    topped_up: float = Field(default=0, ge=0)
    low: bool = False


class BalanceImportRequest(ApiModel):
    provider_id: str = Field(default="deepseek", max_length=80)
    available: bool = True
    fetched_at: str | None = None
    low_threshold: float = Field(default=10, ge=0)
    source: str = Field(default="deepseek-balance-dashboard", max_length=80)
    balances: list[BalanceInfo] = Field(default_factory=list, max_length=20)


class ProviderAccountUpdate(ApiModel):
    base_url: str = Field(default="https://api.deepseek.com", min_length=8, max_length=500)
    api_key: str | None = Field(default=None, max_length=500)
    low_balance_threshold: float = Field(default=10, ge=0, le=1_000_000)
    enabled: bool = True
