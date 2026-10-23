---
name: bili-scroll-setup
description: Connect Codex or Hermes Agent to a self-hosted Bili Scroll service through cc-switch or a direct remote MCP configuration. Use during first-time setup, token rotation, Bilibili QR login, cc-switch synchronization, usage import, or DeepSeek balance setup.
metadata:
  hermes:
    tags: [bilibili, setup, mcp, cc-switch]
    category: productivity
---

# Bili Scroll Setup

Do not request or print passwords, TOTP seeds, Bilibili cookies, provider API
keys, or raw device tokens in chat.

## Codex or Hermes through cc-switch

1. Open the Bili Scroll WebUI supplied by the administrator.
2. Sign in and create a named Agent device token under **Advanced**.
3. In cc-switch, add this MCP server and enable both the Codex and Hermes
   columns:

```json
{
  "mcpServers": {
    "bili-scroll": {
      "type": "http",
      "url": "https://YOUR-DOMAIN/api/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_DEVICE_TOKEN"
      }
    }
  }
}
```

4. In cc-switch **Skills → Repository management**, add this repository and
   scan `plugins/bili-scroll-agent/skills`. Enable `bili-scroll` and
   `bili-scroll-setup` for both applications.
5. Restart Codex/Hermes or start a new chat, then run `$bili-scroll` (Codex) or
   `/bili-scroll` (Hermes).

## Direct remote MCP

Codex can use:

```toml
[mcp_servers.bili-scroll]
url = "https://YOUR-DOMAIN/api/mcp"
bearer_token_env_var = "BILI_SCROLL_TOKEN"
default_tools_approval_mode = "prompt"
```

Hermes can use the same endpoint under `mcp_servers` in
`~/.hermes/config.yaml`, with `trust: untrusted`. Keep the Bearer token in the
profile secret store or `.env`; never in a committed config.

## First checks

1. Call `bili_status` and confirm the account and runner state.
2. Open the WebUI **Advanced** page and scan the Bilibili QR code.
3. Run a single manual scan with a small quota.
4. Keep comments, private messages and DM watching disabled until the user
   explicitly enables them through WebUI password + TOTP step-up.

## Usage and balance

To import cc-switch provider/cost history:

1. Set `BILI_SCROLL_TOKEN` and `BILI_SCROLL_SERVER`.
2. Run `py -3 scripts\local_usage_bridge.py` from this repository.
3. Open **Usage & balance** in the Bili Scroll WebUI.

The bridge reads the cc-switch SQLite database in read-only mode and never
exports provider keys. DeepSeek balance is queried natively by the service from
the official `/user/balance` endpoint. The API key is configured by an Admin
or supplied through `DEEPSEEK_API_KEY`; it is never returned to the browser.

