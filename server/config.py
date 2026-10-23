from __future__ import annotations

from functools import lru_cache
from pathlib import Path

from pydantic import Field, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_prefix="BILI_SCROLL_",
        env_file=".env",
        env_file_encoding="utf-8",
        extra="ignore",
    )

    app_name: str = "Bili Scroll Agent"
    environment: str = "development"
    host: str = "127.0.0.1"
    port: int = 8788
    public_url: str = "http://127.0.0.1:8788"
    secure_cookies: bool = False
    data_dir: Path = Path(".data")
    database_url: str | None = None

    master_key: str | None = None
    session_hours: int = 12
    session_idle_minutes: int = 30
    invite_hours: int = 24
    run_token_minutes: int = 45

    codex_bin: str = "codex"
    hermes_bin: str = "hermes"
    agent_timeout_seconds: int = 1800
    agent_output_limit_bytes: int = 2_000_000
    global_concurrency: int = Field(default=1, ge=1, le=8)
    manual_runs_per_day: int = Field(default=3, ge=0, le=50)
    scheduled_runs_per_day: int = Field(default=1, ge=0, le=12)
    retention_days: int = Field(default=365, ge=1, le=3650)

    login_rate_limit_per_minute: int = Field(default=10, ge=1, le=120)
    api_rate_limit_per_minute: int = Field(default=240, ge=30, le=5000)
    bind_outbound_enabled: bool = True
    incoming_dm_poll_seconds: int = Field(default=90, ge=30, le=3600)

    @model_validator(mode="after")
    def derive_paths(self) -> Settings:
        self.data_dir = self.data_dir.expanduser().resolve()
        if not self.database_url:
            self.database_url = f"sqlite+aiosqlite:///{(self.data_dir / 'bili-scroll.db').as_posix()}"
        return self

    @property
    def is_production(self) -> bool:
        return self.environment.lower() in {"production", "prod"}

    @property
    def web_dist(self) -> Path:
        return Path(__file__).resolve().parent.parent / "web" / "dist"

    @property
    def prompts_dir(self) -> Path:
        return Path(__file__).resolve().parent / "prompts"

    @property
    def agent_home(self) -> Path:
        return self.data_dir / "agents"


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    settings = Settings()
    settings.data_dir.mkdir(parents=True, exist_ok=True)
    return settings


def reset_settings_cache() -> None:
    get_settings.cache_clear()
