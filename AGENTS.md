# Repository guide

This repository provides a self-hosted, multi-tenant Bilibili video digest service
for Codex, Hermes Agent, and cc-switch.

## Toolchain

- Backend: Go (`go test ./...`, `go vet ./...`)
- WebUI packages: Bun (`bun install`, `bun run build`); do not add npm lockfiles
- Database: SQLite via `database/sql` and `modernc.org/sqlite`
- Local plugin MCP server: Go stdio binary under `plugins/bili-scroll-agent/bin`

## Verification

```bash
go test ./...
go vet ./...
cd web && bun test && bun run build && bun audit
```

Never make live Bilibili comments or send DMs during automated verification.
Public writes are opt-in and must go through the WebUI password + TOTP step-up.
