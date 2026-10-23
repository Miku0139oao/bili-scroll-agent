#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "Run as root." >&2
  exit 1
fi

SOURCE_DIR="${1:-$(pwd)}"
DOMAIN="${BILI_SCROLL_DOMAIN:-}"
if [[ -z "${DOMAIN}" ]]; then
  echo "Set BILI_SCROLL_DOMAIN first, for example: BILI_SCROLL_DOMAIN=scroll.example.com ./deploy/install.sh ." >&2
  exit 1
fi

id bili-scroll >/dev/null 2>&1 || useradd --system --home /var/lib/bili-scroll --shell /usr/sbin/nologin bili-scroll
install -d -o bili-scroll -g bili-scroll -m 0700 /var/lib/bili-scroll
install -d -o root -g bili-scroll -m 0750 /etc/bili-scroll
install -d -m 0755 /opt/bili-scroll

rsync -a --delete \
  --exclude .git --exclude .venv --exclude .data --exclude web/node_modules \
  "${SOURCE_DIR}/" /opt/bili-scroll/
chown -R root:bili-scroll /opt/bili-scroll

if ! command -v uv >/dev/null 2>&1; then
  echo "uv is required. Install it from https://docs.astral.sh/uv/ first." >&2
  exit 1
fi
if command -v bun >/dev/null 2>&1; then
  (cd /opt/bili-scroll/web && bun install --frozen-lockfile && bun run build)
else
  echo "Install Bun to build the WebUI." >&2
  exit 1
fi
chown -R bili-scroll:bili-scroll /opt/bili-scroll/web/dist
(cd /opt/bili-scroll && uv sync --frozen --no-dev)
chown -R root:bili-scroll /opt/bili-scroll/.venv

if [[ ! -f /etc/bili-scroll/bili-scroll.env ]]; then
  install -m 0640 -g bili-scroll deploy/env.example /etc/bili-scroll/bili-scroll.env
  KEY="$(python3 -c 'import base64,os; print(base64.b64encode(os.urandom(32)).decode())')"
  sed -i "s|REPLACE_WITH_BASE64_32_BYTES|${KEY}|" /etc/bili-scroll/bili-scroll.env
  sed -i "s|https://YOUR-DOMAIN|https://${DOMAIN}|" /etc/bili-scroll/bili-scroll.env
fi

set -a
# shellcheck disable=SC1091
source /etc/bili-scroll/bili-scroll.env
set +a
(cd /opt/bili-scroll && runuser -u bili-scroll -- .venv/bin/alembic upgrade head)

install -m 0644 deploy/bili-scroll.service /etc/systemd/system/bili-scroll.service
systemctl daemon-reload
systemctl enable --now bili-scroll

cat > /etc/caddy/Caddyfile.bili-scroll <<EOF
$(sed "s/YOUR-DOMAIN/${DOMAIN}/g" deploy/Caddyfile)
EOF
echo "App started. Merge /etc/caddy/Caddyfile.bili-scroll into Caddy, then reload Caddy."
echo "Create the first admin:"
echo "  cd /opt/bili-scroll && runuser -u bili-scroll -- .venv/bin/bili-scroll create-admin"

