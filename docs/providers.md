# Provider adapters

Every AI provider the deployment routes its agent runners through can be
registered in **Admin → Providers**. Each provider has a *kind* that selects an
adapter, a base URL, an encrypted API key, a low-balance threshold, and an
optional egress allowlist.

`provider_id` is the primary key and appears in URLs and in the encryption
associated data (`provider:<id>`), so it is fixed once created:
`^[a-z0-9][a-z0-9._-]{0,63}$`, excluding the reserved words `kinds`, `check`,
`validate`, and `script`.

## Built-in kinds

| Kind | Balance source | Notes |
|---|---|---|
| `deepseek` | `GET {base}/user/balance` | `balance_infos[].{currency,total_balance,granted_balance,topped_up_balance}` plus top-level `is_available`; default currency CNY |
| `openrouter` | `GET {base}/credits` | `Total = total_credits − total_usage`; reports USD |
| `openai-compatible` | none | For usage tracking only; the UI shows "no balance API" |
| `none` | none | Runner routing only |
| `js` | admin script | See below |

`base_url` must be `https`, without credentials, query, or fragment. Plain
`http` is accepted only for loopback addresses, so a local gateway can be
tested. A path prefix is allowed for providers such as OpenRouter
(`https://openrouter.ai/api/v1`).

## JavaScript adapters

A `kind=js` provider carries a script (≤ 64 KiB) that must define one function:

```js
function fetchBalance(ctx) {
  const res = ctx.http.get(ctx.baseUrl + "/user/balance", {
    headers: { Authorization: "Bearer " + ctx.apiKey }
  });
  if (res.status === 401) throw new Error("key rejected");
  if (res.status >= 400) throw new Error("HTTP " + res.status);
  return (res.json.balance_infos || []).map(function (item) {
    return {
      currency: item.currency || "CNY",
      total: Number(item.total_balance),
      granted: Number(item.granted_balance || 0),
      topped_up: Number(item.topped_up_balance || 0),
      available: res.json.is_available !== false
    };
  });
}
```

### The host surface

The only values an adapter receives are inside `ctx`:

| Member | Behaviour |
|---|---|
| `ctx.providerId`, `ctx.baseUrl`, `ctx.apiKey`, `ctx.currency` | plain strings; `apiKey` is `""` when unset |
| `ctx.settings` | the provider's `settings` JSON object |
| `ctx.http.get(url, opts?)` | returns `{status, headers, body, json}` |
| `ctx.http.post(url, body, opts?)` | an object body is sent as JSON |
| `ctx.http.request({method, url, headers, body, json, throw})` | general form; `throw: true` converts ≥400 into an exception |
| `ctx.log(message)` | appended to a capped buffer returned as `adapter_log` by the check endpoint |
| `ctx.now()` | RFC 3339 UTC timestamp |

Execution is **synchronous**: `fetchBalance` must return an array directly.
There is no event loop, so Promises and timers are not available.

### What is not available

`require`, `module`, `exports`, `process`, `console`, `fetch`,
`XMLHttpRequest`, `WebSocket`, `Buffer`, `setTimeout`, `setInterval`, and
`setImmediate` are removed from the runtime. There is no file, environment,
process, or raw-socket access. Standard language globals (`Object`, `Array`,
`JSON`, `Math`, `Date`, …) remain.

### Limits

- 64 KiB script, 32 KiB per run of `ctx.log` output is capped at 8 KiB
- 32 balances per fetch; `total` must be a finite number
- 1 MiB per HTTP response, 512 KiB per fetch, 20 s per fetch (15 s of script)
- recursion is capped; runaway loops are interrupted

### Trust model

**Adapter scripts are admin-authored code and are trusted.** A script has to
receive the API key to call the provider, so an administrator who writes a
hostile adapter can always exfiltrate that key. What the sandbox removes is
*ambient* capability — no filesystem, no environment, no process spawning, no
module loading — and it confines egress to the provider's `allow_hosts` (by
default just the host of `base_url`). That prevents an accidental or careless
script from shipping credentials somewhere else; it does not protect against a
deliberately malicious administrator.

Every save is audited with a script hash and the allowlist; every check reports
which hosts were actually contacted. `BILI_SCROLL_DISABLE_JS_ADAPTERS=1` turns
the kind off entirely.

## Credential precedence

For provider `<id>`:

1. `BILI_SCROLL_PROVIDER_<ID>_API_KEY` / `_BASE_URL` (id upper-cased, `-` and
   `.` become `_`)
2. `DEEPSEEK_API_KEY` / `DEEPSEEK_BASE_URL` when `<id>` is `deepseek`
3. the encrypted key stored in `provider_accounts` (AAD `provider:<id>`)
4. the stored base URL

The key is write-only in the API: responses expose `has_api_key` only.

## API

```
GET    /api/admin/provider-kinds
GET    /api/admin/providers
POST   /api/admin/providers
GET    /api/admin/providers/{id}
PUT    /api/admin/providers/{id}
DELETE /api/admin/providers/{id}
POST   /api/admin/providers/{id}/check
POST   /api/admin/providers/{id}/validate
```

`PUT /api/admin/providers/deepseek` and
`POST /api/admin/providers/deepseek/check` are the pre-0.3 URLs and are served
by the same generic handlers. `GET /api/admin/providers` keeps emitting the
legacy `deepseek` object next to `providers` for the same reason: the SPA is
cached for an hour, so an older bundle can still be in use after an upgrade.

Member-facing:

- `POST /api/me/balance/refresh` with an optional `{"provider": "id"}`; an
  empty body refreshes every enabled provider.
- `GET /api/me/usage` includes `balances` (current, provider-global) and
  `provider_catalog`.

MCP tools: `bili_balance_refresh` (`{provider}`, `{all: true}`, or neither for
the legacy single-provider behaviour) and `bili_providers`, which never exposes
base URLs, scripts, or keys.
