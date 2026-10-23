# Linux VPS + Caddy

Target: Ubuntu/Debian, Go, Bun, systemd and Caddy.

## Install

```bash
BILI_SCROLL_DOMAIN=scroll.example.com sudo -E ./deploy/install.sh .
```

The installer creates:

```text
/opt/bili-scroll/bin/bili-scroll-server
/opt/bili-scroll/bin/bili-scroll-usage-sync
/opt/bili-scroll/plugins/bili-scroll-agent/bin/bili-scroll-mcp
/var/lib/bili-scroll/bili-scroll.db
/etc/bili-scroll/bili-scroll.env
/etc/systemd/system/bili-scroll.service
/etc/caddy/Caddyfile.bili-scroll
```

It builds the React WebUI with Bun and copies it into
`internal/webui/dist` before compiling the Go server, so WebUI assets are
embedded in the server binary.

## Caddy

Include the generated site file from the system Caddyfile:

```caddyfile
import /etc/caddy/Caddyfile.bili-scroll
```

Then validate and reload:

```bash
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

The Go service listens only on `127.0.0.1:8788`. Caddy owns TLS, HSTS,
security headers, the request-body ceiling and HTTPS. The reverse proxy uses
`flush_interval -1` for SSE and Streamable HTTP MCP.

## First admin

```bash
sudo -u bili-scroll \
  env HOME=/var/lib/bili-scroll \
  /opt/bili-scroll/bin/bili-scroll-server create-admin
```

Scan the terminal QR code or enter the displayed Base32 secret in a TOTP
authenticator. Codes use SHA1, six digits and a 30-second period.

Rotate TOTP:

```bash
sudo -u bili-scroll \
  env HOME=/var/lib/bili-scroll \
  /opt/bili-scroll/bin/bili-scroll-server reset-totp --username USERNAME
```

## Runner homes

Set these only when auto-detection is not sufficient:

```dotenv
BILI_SCROLL_CODEX_HOME=/var/lib/bili-scroll/agents/codex
BILI_SCROLL_HERMES_HOME=/var/lib/bili-scroll/agents/hermes
```

Codex:

```bash
sudo -u bili-scroll \
  env CODEX_HOME=/var/lib/bili-scroll/agents/codex \
  codex login
```

Hermes:

```bash
sudo -u bili-scroll \
  env HERMES_HOME=/var/lib/bili-scroll/agents/hermes \
  hermes setup
```

## Operations

```bash
sudo systemctl status bili-scroll
sudo systemctl restart bili-scroll
sudo journalctl -u bili-scroll -f
sudo deploy/backup.sh
sudo deploy/update.sh
```

Health checks:

```bash
curl -fsS http://127.0.0.1:8788/api/health
curl -fsS https://scroll.example.com/api/health
```

## Upgrade and rollback

`update.sh` pulls the current branch, rebuilds WebUI and Go binaries, then
restarts the service. Database tables are created idempotently at startup.

Before upgrading:

```bash
sudo deploy/backup.sh
```

Rollback the Git revision and run:

```bash
cd /opt/bili-scroll
git switch --detach <known-good-sha>
sudo deploy/update.sh
```

Keep the pre-upgrade database snapshot until smoke tests pass.
