# Bili Scroll Agent

Go 實作的 B 站每日選題服務，同時提供：

- `bili-scroll-server`：VPS／本機常駐服務、SQLite、排程器、WebUI 與 Streamable HTTP MCP。
- `bili-scroll-mcp`：Codex／Hermes 真正的本地 stdio MCP plugin，不需要 Python 常駐服務。
- 共用 Agent Skill 與 Codex plugin manifest。
- cc-switch 用量同步器（Go）。
- Caddy + systemd Linux 部署包。

功能基準來自 `c1overzz/dsh-bili-scroll` v2.0.4，B 站整合基準包含
`adoresever/bilibili-mcp`。兩者皆為 MIT 授權，詳見 `NOTICE.md`。

## 架構

```text
cmd/
  bili-scroll-server/     # 多使用者 HTTP 服務／WebUI／排程器
  bili-scroll-mcp/        # 單使用者 stdio MCP plugin
  bili-scroll-usage-sync/ # 唯讀同步 cc-switch 用量
internal/
  app/                    # auth、CSRF、TOTP、API、MCP、scheduler
  bilibili/               # B 站 QR 登入、搜尋、字幕、留言、收藏與私信
  config/                 # 環境設定與 master key
  model/                  # domain models 與固定 RunSummary schema
  runner/                 # Codex／Hermes subprocess adapter
  security/               # AES-256-GCM、Argon2id、TOTP
  store/                  # SQLite schema 與 transaction
plugins/bili-scroll-agent # Skill + portable plugin + local MCP manifest
web/                      # React、TypeScript、Vite、Bun
deploy/                   # systemd、Caddy、安裝、更新、備份
```

後端不依賴 Python、uv、FastAPI、SQLAlchemy 或 Alembic。

## 本機啟動

需求：

- Go 1.22+
- Bun
- Codex 與／或 Hermes Agent

完整建置並啟動 server：

```powershell
cd D:\deepseek-bilbili-codex
powershell -ExecutionPolicy Bypass -File .\scripts\start-dev.ps1
```

開啟 <http://127.0.0.1:8788>。

只建置：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build.ps1
```

產物：

```text
bin/bili-scroll-server.exe
bin/bili-scroll-usage-sync.exe
plugins/bili-scroll-agent/bin/bili-scroll-mcp.exe
plugins/bili-scroll-agent/bin/bili-scroll-mcp
```

手動啟動 server：

```powershell
.\bin\bili-scroll-server.exe serve
```

## 第一次建立 Admin／TOTP

```powershell
.\bin\bili-scroll-server.exe create-admin `
  --username miku0139 `
  --display-name miku0139
```

終端會顯示 QR Code、Base32 secret 與 `otpauth://` URI。

1. 用 Google Authenticator、Microsoft Authenticator、1Password 或其他 TOTP App。
2. 選「新增帳號」→「掃描 QR Code」。
3. 無法掃碼時選「手動輸入設定金鑰」，貼上終端 secret。
4. 登入時輸入帳號、密碼與目前六位數；代碼每 30 秒更新。
5. TOTP 驗證依賴正確時間，手機需開啟自動時間校正。

重設：

```powershell
.\bin\bili-scroll-server.exe reset-totp --username miku0139
```

這會撤銷該使用者既有的 Web session。

## Codex plugin

Plugin 位於 [`plugins/bili-scroll-agent`](plugins/bili-scroll-agent)，包含：

- 標準 `SKILL.md`
- portable `plugin.json`
- `.codex-plugin/plugin.json` 相容層
- `mcp.json` stdio server manifest
- 本地 `bili-scroll-mcp` binary

它不使用遠端 server，也不要求 WebUI。資料預設寫入 Codex 提供的
`PLUGIN_DATA`，首次啟動會建立 `local` profile。

本機 marketplace：

```powershell
codex plugin marketplace add D:\deepseek-bilbili-codex
codex plugin add bili-scroll-agent@bili-scroll-local
```

安裝後開新對話使用 `$bili-scroll`。第一次使用時呼叫
`bili_login_start`，掃描 B 站 QR 後以 `bili_login_poll` 完成登入。

Hermes 使用同一份 `skills/` 與 `mcp.json`，不需要另一套業務 plugin。

## 遠端 server + WebUI

Server 提供多租戶、邀請、密碼 + TOTP、排程、執行紀錄、日報、收藏、
評論／私信控制、用量與 DeepSeek 餘額。

MCP endpoint：

```text
https://YOUR-DOMAIN/api/mcp
```

遠端 MCP 使用個人 Bearer device token；Codex／Hermes 子程序只取得短效
run token。B 站 cookie、TOTP secret 與 provider key 永遠不會進入模型
context。

## cc-switch

Codex MCP 片段、Hermes YAML、同一份 Skill 與用量同步說明位於
[`integrations/cc-switch`](integrations/cc-switch)。

開始同步：

```powershell
$env:BILI_SCROLL_SERVER = "http://127.0.0.1:8788"
$env:BILI_SCROLL_TOKEN = "bili_device_..."
.\bin\bili-scroll-usage-sync.exe --watch
```

同步器以唯讀模式查詢 `~/.cc-switch/cc-switch.db`，每 60 秒上傳每日
provider/model/token/cost 彙總。它不會讀取或上傳 provider API key。

WebUI 的 Usage 頁顯示：

- 每次 run 的 provider/model、tokens、成本與 exit code
- cc-switch 歷史 provider/model 用量
- cache hit rate
- DeepSeek 官方 `/user/balance`

Admin 的 provider/model 留空時，runner 沿用本機 `CODEX_HOME` 或
`HERMES_HOME` 設定，因此會直接使用 cc-switch 切換後的路由。

## Runner

排程與手動執行只接受固定 executable：Codex 或 Hermes。

Codex：

```text
codex exec --ephemeral --skip-git-repo-check ... --json -
```

Hermes：

```text
hermes --usage-file <file> chat --oneshot --quiet --query-file <file> --source tool
```

兩者不使用 shell，具 timeout、輸出大小限制與獨立暫存工作目錄。
服務重啟會清除孤兒 run；關閉服務會終止執行中的子程序。

## 安全邊界

- Invite-only；Admin／Member 角色。
- Argon2id 密碼、TOTP、HttpOnly/SameSite session、CSRF。
- AES-256-GCM 加密 B 站 cookie、TOTP secret 與 provider key。
- API token 只存 HMAC-SHA256 摘要；完整值只顯示一次。
- 評論、私信與 DM 監聽預設關閉，WebUI 需密碼 + TOTP step-up。
- B 站標題、簡介、字幕、留言與私訊均視為不可信資料。
- MCP 唯讀／寫入工具提供 annotations；明確寫入需要 `confirm=true`。

## 測試

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\test.ps1
go test -race ./...
go vet ./...
cd web
bun test
bun audit
```

自動測試不產生公開評論或私信。

## Linux + Caddy

`deploy/install.sh` 會：

- 建立 `bili-scroll` service account
- 用 Bun 建置 WebUI
- 用 Go 建置 server、usage sync 與 plugin binary
- 將 WebUI embed 進 Go server
- 安裝 systemd unit
- 產生 Caddy site 片段

Go server 只綁 `127.0.0.1:8788`。Caddy 提供 TLS、HSTS、安全標頭、
request body limit 與 reverse proxy；`flush_interval -1` 保留 SSE／MCP
串流。

```bash
BILI_SCROLL_DOMAIN=scroll.example.com sudo -E ./deploy/install.sh .
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

參見 [`deploy/README.md`](deploy/README.md) 與 [`deploy/Caddyfile`](deploy/Caddyfile)。

要交給另一台 Linux 機器上的部署 Agent，直接使用
[`docs/deploy-handoff.md`](docs/deploy-handoff.md) 的完整提示詞。

## 授權

MIT。授權與 upstream attribution 見 [`LICENSE`](LICENSE) 與
[`NOTICE.md`](NOTICE.md)。
