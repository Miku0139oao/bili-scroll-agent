from __future__ import annotations

import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from fastapi.responses import FileResponse, HTMLResponse, JSONResponse
from fastapi.staticfiles import StaticFiles

from . import __version__
from .config import get_settings
from .crypto import SecretCipher
from .db import Database
from .events import EventHub
from .mcp_server import create_mcp
from .ratelimit import RateLimiter
from .routers import admin, auth, ingest, me
from .runtime import resolve_bearer
from .services.scheduler import SchedulerService

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
LOGGER = logging.getLogger(__name__)

settings = get_settings()
db = Database(settings)
cipher = SecretCipher(settings)
events = EventHub()
scheduler = SchedulerService(db, settings, cipher, events)
mcp = create_mcp(db, settings, cipher, scheduler)
mcp_app = mcp.streamable_http_app()


@asynccontextmanager
async def lifespan(app: FastAPI):
    await db.initialize()
    await scheduler.recover_stale_runs()
    async with mcp.session_manager.run():
        await scheduler.start()
        LOGGER.info("Bili Scroll Agent %s started", __version__)
        try:
            yield
        finally:
            await scheduler.stop()
            await db.close()


app = FastAPI(
    title=settings.app_name,
    version=__version__,
    docs_url="/api/docs" if not settings.is_production else None,
    redoc_url=None,
    openapi_url="/api/openapi.json" if not settings.is_production else None,
    lifespan=lifespan,
)
app.state.settings = settings
app.state.db = db
app.state.cipher = cipher
app.state.scheduler = scheduler
app.state.events = events
app.state.rate_limiter = RateLimiter()
app.state.version = __version__

app.include_router(auth.router, prefix="/api")
app.include_router(me.router, prefix="/api")
app.include_router(admin.router, prefix="/api")
app.include_router(ingest.router, prefix="/api")
app.mount("/api", mcp_app)


@app.middleware("http")
async def security_middleware(request: Request, call_next):
    if request.url.path.startswith("/api/mcp"):
        async with db.session() as session:
            owner = await resolve_bearer(request, session, request.headers.get("authorization"))
        if not owner:
            return JSONResponse(
                {"detail": "valid Bearer token required"},
                status_code=401,
                headers={"WWW-Authenticate": "Bearer"},
            )
    if request.url.path.startswith("/api/") and not request.url.path.startswith(
        ("/api/mcp", "/api/auth/login", "/api/auth/invite")
    ):
        key = f"api:{request.client.host if request.client else 'unknown'}"
        if not await request.app.state.rate_limiter.allow(
            key, request.app.state.settings.api_rate_limit_per_minute
        ):
            return JSONResponse({"detail": "rate limit exceeded"}, status_code=429)
    response = await call_next(request)
    response.headers.setdefault("X-Content-Type-Options", "nosniff")
    response.headers.setdefault("X-Frame-Options", "DENY")
    response.headers.setdefault("Referrer-Policy", "strict-origin-when-cross-origin")
    response.headers.setdefault("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
    response.headers.setdefault(
        "Content-Security-Policy",
        "default-src 'self'; img-src 'self' data: https://*.hdslb.com; "
        "style-src 'self' 'unsafe-inline'; script-src 'self'; "
        "connect-src 'self'; frame-ancestors 'none'; base-uri 'self'",
    )
    if settings.secure_cookies:
        response.headers.setdefault("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
    return response


@app.get("/api/health")
async def public_health() -> dict:
    return {"ok": True, "service": "bili-scroll-agent", "version": __version__}


assets = settings.web_dist / "assets"
if assets.exists():
    app.mount("/assets", StaticFiles(directory=assets), name="assets")


@app.get("/{path:path}", include_in_schema=False)
async def spa(path: str):
    if path.startswith("api/"):
        return JSONResponse({"detail": "not found"}, status_code=404)
    index = settings.web_dist / "index.html"
    if index.exists():
        return FileResponse(index)
    return HTMLResponse(
        """<!doctype html><html lang="zh-Hant"><meta charset="utf-8">
<title>Bili Scroll Agent</title>
<body style="font:16px system-ui;max-width:760px;margin:10vh auto;padding:24px">
<h1>Bili Scroll Agent</h1>
<p>後端已啟動，但 WebUI 尚未建置。請先執行 <code>cd web &amp;&amp; bun install &amp;&amp; bun run build</code>。</p>
<p><a href="/api/health">API health</a></p></body></html>""",
        status_code=503,
    )
