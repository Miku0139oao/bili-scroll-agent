from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from pathlib import Path

from sqlalchemy import event
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
    create_async_engine,
)

from .config import Settings
from .models import Base, SystemSetting


class Database:
    def __init__(self, settings: Settings):
        self.settings = settings
        self.engine: AsyncEngine = create_async_engine(
            settings.database_url or "",
            pool_pre_ping=True,
            connect_args={"timeout": 30},
        )
        self.sessions = async_sessionmaker(self.engine, expire_on_commit=False)

        @event.listens_for(self.engine.sync_engine, "connect")
        def _sqlite_pragmas(dbapi_connection, _record) -> None:  # noqa: ANN001
            cursor = dbapi_connection.cursor()
            cursor.execute("PRAGMA journal_mode=WAL")
            cursor.execute("PRAGMA synchronous=NORMAL")
            cursor.execute("PRAGMA foreign_keys=ON")
            cursor.execute("PRAGMA busy_timeout=30000")
            cursor.close()

    async def initialize(self) -> None:
        Path(self.settings.data_dir).mkdir(parents=True, exist_ok=True)
        async with self.engine.begin() as connection:
            await connection.run_sync(Base.metadata.create_all)

    @asynccontextmanager
    async def session(self) -> AsyncIterator[AsyncSession]:
        async with self.sessions() as session:
            try:
                yield session
                await session.commit()
            except Exception:
                await session.rollback()
                raise

    async def get_setting(self, session: AsyncSession, key: str, default: dict | None = None) -> dict:
        value = await session.get(SystemSetting, key)
        return dict(value.value) if value else dict(default or {})

    async def set_setting(self, session: AsyncSession, key: str, value: dict) -> None:
        row = await session.get(SystemSetting, key)
        if row:
            row.value = value
        else:
            session.add(SystemSetting(key=key, value=value))

    async def close(self) -> None:
        await self.engine.dispose()
