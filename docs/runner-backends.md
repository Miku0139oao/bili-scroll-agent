# Runner backends

The service starts an agent CLI as a subprocess, hands it a run-scoped token,
and waits for the agent to commit the run through the MCP tools
`bili_finish_run` (digest runs) or `bili_finish_dm_reply` (DM replies).

## The completion contract

A run is completed **only** by an MCP tool call. There is deliberately no
stdout-based fallback: the summary is validated against the run's candidate
list, the tier whitelist, and the daily quota, and a text-parsing path would let
an agent claim success without any of that.

The consequence is a hard requirement:

> A backend can only run digest or DM jobs if its CLI can call an HTTP MCP
> server with a bearer token.

A CLI that cannot do that may still be registered, but it can only support
read-only work. This is why the availability check is capability-based, not just
"is the binary installed".

## Built-in backends

| Name | Invocation | Completion |
|---|---|---|
| `codex` | `codex exec --ephemeral --skip-git-repo-check -C <work> --disable plugins -c mcp_servers.bili_scroll.* --json -` | HTTP MCP via the `CODEX_HOME` config the runner injects |
| `hermes` | `hermes --usage-file <work>/usage.json chat --oneshot --quiet --query-file <work>/prompt.txt --source tool` | HTTP MCP from the Hermes profile |
| `claude` | `claude -p <prompt> --output-format stream-json --verbose --mcp-config <work>/mcp.json --strict-mcp-config --allowedTools mcp__bili_scroll` | HTTP MCP from the per-run `mcp.json` |

All three run with an isolated config directory (`CODEX_HOME`,
`HERMES_HOME`, `CLAUDE_CONFIG_DIR`) and a fresh temporary work directory that is
deleted afterwards. The prompt never goes through a shell.

### Claude Code specifics

`mcp.json` is written per run (mode 0600) and deleted with the work directory:

```json
{
  "mcpServers": {
    "bili_scroll": {
      "type": "http",
      "url": "http://127.0.0.1:8788/api/mcp",
      "headers": { "Authorization": "Bearer <run token>" }
    }
  }
}
```

- `CLAUDE_CONFIG_DIR` points at `DataDir/agents/claude` (or
  `BILI_SCROLL_CLAUDE_HOME`), so user settings and plugins do not leak in.
- The `provider` route is applied as `ANTHROPIC_BASE_URL` only when it is a
  full URL; a bare provider name is ignored and reported as a run warning.
- `reasoning` is not supported and is reported as a run warning.
- Claude Code's flags move between releases. The availability probe runs
  `claude --help` once and reports the backend as unavailable with a reason
  (`missing --mcp-config`, …) when a required flag is absent, instead of failing
  every run.

## Configuration

```
BILI_SCROLL_CODEX_BIN / BILI_SCROLL_CODEX_HOME
BILI_SCROLL_HERMES_BIN / BILI_SCROLL_HERMES_HOME
BILI_SCROLL_CLAUDE_BIN / BILI_SCROLL_CLAUDE_HOME
```

Per-runner provider/model/reasoning is stored in `runner_routes` in the admin
settings JSON:

```json
{
  "default_runner": "codex",
  "runner_routes": {
    "codex": { "provider": "deepseek", "model": "deepseek-chat", "reasoning": "high" },
    "claude": { "provider": "https://gateway.example.com", "model": "claude-sonnet-4-6" }
  }
}
```

An empty field means "leave the CLI's own configuration alone". The deprecated
flat `codex_provider` / `codex_model` / `codex_reasoning` / `hermes_provider` /
`hermes_model` keys are still read (folded into `runner_routes`) and written
(mirrored back) so older clients keep working.

## Adding a backend

A backend is a descriptor in `internal/runner/backend.go`:

```go
type Backend struct {
    Name       string
    Label      string
    Bin        func(*config.Settings) string
    Candidates func(*config.Settings) []string
    HomeEnv    string
    HomeFor    func(*config.Settings) string
    Build      func(*config.Settings, Request, string) (Invocation, error)
    Probe      func(executable string) (string, bool) // optional capability check
}
```

`Build` returns argv, optional files to write into the work directory, extra
environment, and a `Usage` callback that extracts tokens and cost after exit.
Register it in `All()`. `Status()` and `Names()` pick it up automatically, and
the WebUI runner select and admin route editor iterate the registry, so no
frontend change is needed.

Write a golden test for the argv (see `internal/runner/backend_test.go`) before
adding the backend to a running deployment.

## Operational notes

- `BILI_SCROLL_GLOBAL_CONCURRENCY` bounds concurrent subprocesses across every
  path: scheduled, manual, MCP-triggered, and DM. A run that cannot get a slot
  waits in order. The semaphore is sized at startup.
- Run tokens expire after `BILI_SCROLL_RUN_TOKEN_MINUTES` (45 by default). An
  agent timeout above ~45 minutes will make late MCP calls fail with 401.
- A run whose heartbeat is older than twice `BILI_SCROLL_AGENT_TIMEOUT_SECONDS`
  is failed by the watchdog, releasing the per-user active-run slot.
