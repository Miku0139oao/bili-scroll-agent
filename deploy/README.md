# Linux VPS deployment

The installer targets Ubuntu/Debian and uses Bun for the WebUI, `uv` for the
Python service, systemd for process supervision, and Caddy for TLS.

## Install

```bash
BILI_SCROLL_DOMAIN=scroll.example.com sudo -E ./deploy/install.sh .
```

The installer:

- creates the `bili-scroll` service account;
- copies the app to `/opt/bili-scroll`;
- builds the WebUI with Bun;
- syncs Python dependencies with `uv`;
- creates `/etc/bili-scroll/bili-scroll.env`;
- applies Alembic migrations;
- enables the systemd unit.

Merge `deploy/Caddyfile` (with the domain substituted) into `/etc/caddy` and
reload Caddy. Do not expose port 8788 directly.

## First admin

```bash
sudo -u bili-scroll \
  env BILI_SCROLL_DATA_DIR=/var/lib/bili-scroll \
  /opt/bili-scroll/.venv/bin/bili-scroll create-admin
```

Store the displayed TOTP secret in the authenticator immediately. It is not
shown again by the WebUI.

To rotate a lost or exposed administrator TOTP secret:

```bash
sudo -u bili-scroll \
  env BILI_SCROLL_DATA_DIR=/var/lib/bili-scroll \
  /opt/bili-scroll/.venv/bin/bili-scroll reset-totp --username <admin-username>
```

This also revokes the user's existing browser sessions and temporary setup
tokens.

## Agent runner setup

The scheduler calls Codex and/or Hermes under the `bili-scroll` service
account. Their credentials are independent from desktop cc-switch.

Codex:

```bash
install -d -o bili-scroll -g bili-scroll -m 0700 \
  /var/lib/bili-scroll/agents/codex
install -o bili-scroll -g bili-scroll -m 0600 \
  deploy/codex-runner.config.toml \
  /var/lib/bili-scroll/agents/codex/config.toml

sudo -u bili-scroll \
  env CODEX_HOME=/var/lib/bili-scroll/agents/codex codex login
```

Hermes:

```bash
install -d -o bili-scroll -g bili-scroll -m 0700 \
  /var/lib/bili-scroll/agents/hermes
install -o bili-scroll -g bili-scroll -m 0600 \
  deploy/hermes-runner.config.yaml \
  /var/lib/bili-scroll/agents/hermes/config.yaml

sudo -u bili-scroll \
  env HERMES_HOME=/var/lib/bili-scroll/agents/hermes hermes setup
```

Both templates point at the loopback MCP endpoint and read the per-run Bearer
token from `BILI_SCROLL_TOKEN`, which the service injects into the child
process. The token is expired after the run and never appears in the prompt.

## DeepSeek balance

Save the DeepSeek API key in **Admin → DeepSeek Provider**, or add
`DEEPSEEK_API_KEY` to `/etc/bili-scroll/bili-scroll.env`. The key is used only
for the official `/user/balance` endpoint.

## Operations

```bash
sudo deploy/backup.sh
sudo systemctl restart bili-scroll
sudo deploy/update.sh
journalctl -u bili-scroll -f
```

