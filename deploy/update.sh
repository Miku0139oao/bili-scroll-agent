#!/usr/bin/env bash
set -euo pipefail

cd /opt/bili-scroll
git pull --ff-only
if command -v bun >/dev/null 2>&1; then
  (cd web && bun install --frozen-lockfile && bun run build)
else
  echo "Bun is required to build the WebUI." >&2
  exit 1
fi
cp -a web/dist/. internal/webui/dist/
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/bili-scroll-server ./cmd/bili-scroll-server
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/bili-scroll-usage-sync ./cmd/bili-scroll-usage-sync
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
  -o plugins/bili-scroll-agent/bin/bili-scroll-mcp ./cmd/bili-scroll-mcp
set -a
# shellcheck disable=SC1091
source /etc/bili-scroll/bili-scroll.env
set +a
systemctl restart bili-scroll
systemctl --no-pager --full status bili-scroll

