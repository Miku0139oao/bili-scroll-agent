# Repository guide

This repository provides a self-hosted, multi-tenant Bilibili video digest service
for Codex, Hermes Agent, and cc-switch.

## Toolchain

- Python packages: `uv`
- WebUI packages: Bun (`bun install`, `bun run build`); do not add npm lockfiles
- Database: SQLite via SQLAlchemy async

## Verification

```bash
uv run pytest -q
uv run ruff check server tests scripts
cd web && bun test && bun run build && bun audit
```

Never make live Bilibili comments or send DMs during automated verification.
Public writes are opt-in and must go through the WebUI password + TOTP step-up.
