# Bili Scroll Agent deployment handoff

請把下面整段提示詞交給另一台機器上的部署 Agent。先把尖括號內的資料補齊。

```text
你是 Bili Scroll Agent 的部署與驗收 Agent。請直接操作我提供的目標主機，
完成安裝、Caddy 配置、systemd、安全檢查、基本 smoke test 與交接報告。

## 目標

- Private repository:
  https://github.com/Miku0139oao/bili-scroll-agent
- Target host: <SSH_HOST>
- SSH user: <SSH_USER>
- Public domain: <DOMAIN>
- Contact email for Caddy: <EMAIL>
- Preferred runner: <codex|hermes|both>
- Existing reverse proxy: <yes|no；如果是，先說明現有配置>

## 重要邊界

1. 不要修改或輸出任何 B 站 cookie、TOTP secret、provider API key、
   master key 或 device token。
2. 不要把 secret 寫入 Git、shell history、日誌或最終聊天回覆。
3. 不要把 repo 改成 public。
4. 不要清除既有 Caddy 主設定；只新增 include 或明確合併專案片段。
5. 不要建立公開評論、私信、收藏或 DM 回覆。
6. 不要在缺少參數時猜測 domain、DNS、SSH 或 provider 設定；停下來列出具體缺項。
7. 任何覆寫／遷移前先備份，並提供可回滾位置。

## 預期架構

- Go server 只綁 127.0.0.1:8788。
- Caddy 對外提供 HTTPS、HSTS、安全標頭與 /api/mcp 反代。
- systemd 使用專用 bili-scroll service account。
- 原始碼位於 /opt/bili-scroll。
- SQLite 與 agent homes 位於 /var/lib/bili-scroll。
- 環境與 master key 位於 /etc/bili-scroll/bili-scroll.env，權限 0600 root:root。
- WebUI 靜態檔已 embed 進 Go server binary。

## 1. Preflight

先只做唯讀檢查並回報：

- /etc/os-release；必須是 Ubuntu 或 Debian
- uname -m；x86_64 或 arm64
- sudo/root 權限
- Go >= 1.26.0
- Bun
- Caddy v2
- openssl、rsync、curl、git、systemctl
- DNS A/AAAA 是否已指向免費目標主機
- 80、443 是否可用
- 8788 是否已被其他程式佔用
- Caddy 目前的 service 狀態與設定路徑
- root 是否已有讀取 private GitHub repo 的 credential 或 deploy key

若缺少任何依賴，先安裝或回報，不要直接執行 installer。

## 2. 備份與下載

- 備份現有 Caddyfile 與 systemd units。
- 將 repo clone 到 /srv/bili-scroll-src，例如：

  sudo git clone https://github.com/Miku0139oao/bili-scroll-agent.git /srv/bili-scroll-src

- 確認 clone 的 HEAD 與 main 一致。
- 檢查 git status 必須乾淨。

## 3. 安裝

在 repo root 執行：

  BILI_SCROLL_DOMAIN=<DOMAIN> sudo -E ./deploy/install.sh .

安裝後檢查：

- /opt/bili-scroll/bin/bili-scroll-server
- /opt/bili-scroll/bin/bili-scroll-usage-sync
- /opt/bili-scroll/plugins/bili-scroll-agent/bin/bili-scroll-mcp
- /var/lib/bili-scroll
- /etc/bili-scroll/bili-scroll.env
- /etc/systemd/system/bili-scroll.service
- /etc/caddy/Caddyfile.bili-scroll

確認：

- env file owner/group/mode 是 root:root 0600
- data directory owner/group 是 bili-scroll:bili-scroll 0700
- service 只 listen 127.0.0.1:8788
- systemd 狀態 active

## 4. Caddy

- 在主 Caddyfile 加入：

  import /etc/caddy/Caddyfile.bili-scroll

- 保留既有網站與 upstream。
- 執行：

  sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
  sudo systemctl reload caddy

- 驗證：

  curl -fsS https://<DOMAIN>/api/health

必須回傳 service=bili-scroll-agent 與 version=0.2.0-go。

## 5. 第一位 Admin

請由使用者在安全終端自行執行，或確保 QR/secret 不會被記錄：

  sudo -u bili-scroll env HOME=/var/lib/bili-scroll \
    /opt/bili-scroll/bin/bili-scroll-server create-admin \
    --username <ADMIN_USERNAME> --display-name <DISPLAY_NAME>

使用者需用 Google Authenticator、Microsoft Authenticator、1Password
或其他 TOTP App 掃描 QR；無法掃碼時手動輸入 Base32 secret。
TOTP 是 6 位數、30 秒、SHA1。

不要代填密碼或 TOTP，也不要把 secret 放到聊天或 log。

## 6. Runner

若選 Codex：

- 確認 service account 可執行 codex
- 設定 CODEX_HOME=/var/lib/bili-scroll/agents/codex
- 由 Admin 在服務端完成 Codex provider/auth
- 不要在 VPS 反向依賴桌機 cc-switch

若選 Hermes：

- 確認 service account 可執行 hermes
- 設定 HERMES_HOME=/var/lib/bili-scroll/agents/hermes
- 由 Admin 完成 Hermes provider/auth

在 WebUI Admin 設定可用 provider/model；留空代表 runtime-default。
如果 runner 實測不可用，不要啟用排程，回報 missing dependency。

## 7. WebUI 驗收

- 開啟 https://<DOMAIN>
- 確認登入頁、TOTP 說明與 mobile layout
- Admin 登入
- 建立一次性邀請
- 用非 Admin 測試帳號完成邀請與 TOTP
- 以 Member 掃描自己的 B 站 QR
- 設定關鍵字與時區
- 不開啟評論、私信或 DM 監聽
- 執行手動 run，但不要進行任何公開寫入
- 確認進度、報告、BVID 判重、額度與 usage 頁正常

## 8. cc-switch / MCP

在 WebUI 建立 named device token。

Codex/Hermes 使用：

  URL: https://<DOMAIN>/api/mcp
  Authorization: Bearer <DEVICE_TOKEN>

用 MCP initialize 與 tools/list 驗證：

- initialize 回 2025-03-26
- tools/list 至少包含 bili_status、bili_begin_run、bili_search、
  bili_subtitle、bili_finish_run
- bili_status 只看到目前 Member 自己的資料

不要把 token 寫入 repo 或最終回覆。

## 9. 可靠性驗收

- sudo systemctl restart bili-scroll 後 health 仍正常
- 重啟後 stale run 被標記 failed，不會卡住新 run
- sudo deploy/backup.sh 產出 SQLite 快照
- sudo systemctl reboot 後 service 與 Caddy 自動恢復
- 檢查 journalctl 沒有 secret、cookie 或完整 token
- 檢查 8788 沒有對外 listen
- 驗證 HSTS、CSP、X-Content-Type-Options、X-Frame-Options

## 10. 最終交付

請提供：

1. 使用的 commit SHA
2. OS、arch、Go、Bun、Caddy 版本
3. service/Caddy 狀態
4. 對外 health JSON
5. systemd listen 驗證
6. Caddy validate 結果
7. WebUI、Admin、Member、B 站 QR、MCP 驗收結果
8. backup 路徑與是否完成重啟測試
9. 所有尚未完成或有風險的項目
10. rollback commit、Caddy backup 路徑與 DB backup 路徑

不要因為 CI 顯示失敗就中止部署；先確認失敗是否為 GitHub Actions
billing/spending limit。若是，與部署程式本身無關，應明確回報。

完成所有可執行驗收後，回覆「DEPLOYMENT READY」或「DEPLOYMENT BLOCKED」。
```
