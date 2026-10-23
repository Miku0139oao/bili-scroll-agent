#!/usr/bin/env bash
set -euo pipefail

APP_DIR="${APP_DIR:-/opt/bili-scroll}"
DATA_DIR="${BILI_SCROLL_DATA_DIR:-/var/lib/bili-scroll}"

install -d -o bili-scroll -g bili-scroll -m 0700 "${DATA_DIR}/backups"
cd "${APP_DIR}"
BILI_SCROLL_DATA_DIR="${DATA_DIR}" \
  runuser -u bili-scroll -- bin/bili-scroll-server backup

