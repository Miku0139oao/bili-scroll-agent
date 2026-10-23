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
runuser -u bili-scroll -- uv sync --frozen --no-dev
set -a
# shellcheck disable=SC1091
source /etc/bili-scroll/bili-scroll.env
set +a
runuser -u bili-scroll -- .venv/bin/alembic upgrade head
systemctl restart bili-scroll
systemctl --no-pager --full status bili-scroll

