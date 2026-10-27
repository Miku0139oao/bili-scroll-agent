# cc-switch 接入

一個 MCP server、兩個 Agent。cc-switch 的 MCP 矩陣可以把
`bili-scroll` 同時同步到 Codex 與 Hermes；Skills 頁則把
`plugins/bili-scroll-agent/skills` 安裝到兩邊。

## 設定 MCP

1. 在 Bili Scroll WebUI 的「進階」建立 Agent 權杖。
2. 在 cc-switch 選「新增 MCP」→「貼上設定」。
3. 使用 `mcp-servers.json`，替換 domain 與 token。
4. 同時啟用 Codex、Hermes 兩欄。

cc-switch 會寫入 `~/.codex/config.toml` 的 `[mcp_servers]` 與
`~/.hermes/config.yaml` 的 `mcp_servers`。本專案不直接修改 cc-switch DB。

## 安裝 Skills

1. cc-switch → Skills → 倉庫管理 → 新增此 repository。
2. 技能路徑填 `plugins/bili-scroll-agent/skills`。
3. 安裝 `bili-scroll` 與 `bili-scroll-setup`，目標同時勾選 Codex、Hermes。

## cc-switch 用量同步

1. 在 Bili Scroll WebUI 建立裝置權杖。
2. 在 PowerShell 設定：

```powershell
$env:BILI_SCROLL_SERVER = "https://YOUR-DOMAIN"
$env:BILI_SCROLL_TOKEN = "bili_device_..."
.\bin\bili-scroll-usage-sync.exe --watch
```

腳本以唯讀方式查詢 `~/.cc-switch/cc-switch.db` 的
`usage_daily_rollups`，只上傳每日彙總的 provider/model/token/cost，
不上傳 provider API key；加上 `--watch` 後每 60 秒更新。

如果 Admin 的 Codex/Hermes Provider 與 Model 留空，服務 runner 會沿用
`CODEX_HOME`／`HERMES_HOME` 的預設 provider；這正是 cc-switch 管理的
Codex TOML 與 Hermes YAML。WebUI 會把這種情況標為 `runtime-default`，
避免把 cc-switch 路由誤寫成服務端硬編碼設定。

餘額由各 provider 的 adapter 原生查詢（DeepSeek `/user/balance`、
OpenRouter `/credits`，或 Admin 自己寫的 JavaScript adapter）。Admin 在
**Admin → Providers** 逐個保存 API key，或由 systemd 提供
`BILI_SCROLL_PROVIDER_<ID>_API_KEY`（DeepSeek 仍接受舊的
`DEEPSEEK_API_KEY`）；Member 在 **Usage & balance** 頁按重新整理即可。
詳見 [`../../docs/providers.md`](../../docs/providers.md)。
