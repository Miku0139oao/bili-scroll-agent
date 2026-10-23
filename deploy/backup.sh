#!/usr/bin/env bash
set -euo pipefail

APP_DIR="${APP_DIR:-/opt/bili-scroll}"
DATA_DIR="${BILI_SCROLL_DATA_DIR:-/var/lib/bili-scroll}"
TARGET="${DATA_DIR}/backups/bili-scroll-$(date -u +%Y%m%d-%H%M%S).db"

mkdir -p "${DATA_DIR}/backups"
cd "${APP_DIR}"
.venv/bin/python -c '
import sqlite3, sys
src, dst = sys.argv[1:3]
with sqlite3.connect(src) as source, sqlite3.connect(dst) as target:
    source.backup(target)
' "${DATA_DIR}/bili-scroll.db" "${TARGET}"
echo "${TARGET}"

