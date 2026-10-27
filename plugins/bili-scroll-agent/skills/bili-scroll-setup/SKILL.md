---
name: bili-scroll-setup
description: Set up the local Bili Scroll plugin or connect Codex and Hermes Agent to a self-hosted Bili Scroll server. Use for first-time setup, Bilibili QR login, TOTP recovery, cc-switch routing, usage import, or provider balances.
---

# Bili Scroll Setup

Do not request or print passwords, TOTP seeds, Bilibili cookies, provider API
keys, or raw device tokens in chat.

## Local plugin

The portable plugin starts `bili-scroll-mcp` over stdio. It creates a local
`local` profile under `PLUGIN_DATA`; no server, WebUI, account token or admin
invitation is required.

1. Call `bili_status`.
2. If Bilibili is not connected, call `bili_login_start`.
3. Show the QR to the user and wait for them to scan it in the Bilibili app.
4. Call `bili_login_poll` with the returned key. Stop polling after expiry.
5. Configure keywords with `bili_config_update`.
6. Start a small test scan manually. Do not call `bili_request_run` from the
   local plugin unless the user explicitly wants a nested server-side agent.

The local plugin stores encrypted credentials in `PLUGIN_DATA`. Never ask for
Bilibili cookies or read the database directly.

## Remote server through cc-switch

1. Open the Bili Scroll WebUI supplied by the administrator.
2. Sign in and create a named Agent device token under **Advanced**.
3. In cc-switch, add the remote MCP server and enable Codex and Hermes:

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
2. Complete Bilibili QR login with `bili_login_start` / `bili_login_poll`.
3. Run a single manual scan with a small quota.
4. Keep comments, private messages and DM watching disabled until the user
   explicitly enables them through WebUI password + TOTP step-up.

## Usage and balance

To import cc-switch provider/cost history:

1. Set `BILI_SCROLL_TOKEN` and `BILI_SCROLL_SERVER`.
2. Run `bin\bili-scroll-usage-sync.exe --watch` from this repository.
3. Open **Usage & balance** in the Bili Scroll WebUI.

The bridge reads the cc-switch SQLite database in read-only mode and never
exports provider keys. Balances are queried by per-provider adapters (their
native endpoints, or a JavaScript adapter an Admin writes). Keys are configured
per provider by an Admin, or supplied through `BILI_SCROLL_PROVIDER_<ID>_API_KEY`;
they are never returned to the browser.

## TOTP onboarding

The admin CLI prints a scannable QR code, the Base32 setup secret, and the
`otpauth://` URI. Invited users receive the same setup material in the WebUI.
Scan the QR or choose **Add account → Enter setup key** in a TOTP
authenticator, then submit the current six-digit code. Codes rotate every 30
seconds, so automatic phone clock synchronization must be enabled.

