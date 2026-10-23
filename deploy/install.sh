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
  --exclude .venv --exclude .data --exclude web/node_modules \
  --exclude bin --exclude internal/webui/dist --exclude plugins/bili-scroll-agent/bin \
  "${SOURCE_DIR}/" /opt/bili-scroll/
chown -R root:bili-scroll /opt/bili-scroll

if ! command -v go >/dev/null 2>&1; then
  echo "Go 1.22+ is required." >&2
  exit 1
fi
if command -v bun >/dev/null 2>&1; then
  (cd /opt/bili-scroll/web && bun install --frozen-lockfile && bun run build)
else
  echo "Install Bun to build the WebUI." >&2
  exit 1
fi
install -d -m 0755 /opt/bili-scroll/internal/webui/dist /opt/bili-scroll/bin \
  /opt/bili-scroll/plugins/bili-scroll-agent/bin
cp -a /opt/bili-scroll/web/dist/. /opt/bili-scroll/internal/webui/dist/
(cd /opt/bili-scroll && \
  CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o bin/bili-scroll-server ./cmd/bili-scroll-server && \
  CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o bin/bili-scroll-usage-sync ./cmd/bili-scroll-usage-sync && \
  CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o plugins/bili-scroll-agent/bin/bili-scroll-mcp ./cmd/bili-scroll-mcp)
chown -R root:bili-scroll /opt/bili-scroll/bin /opt/bili-scroll/plugins/bili-scroll-agent/bin
chown -R bili-scroll:bili-scroll /opt/bili-scroll/web/dist /opt/bili-scroll/internal/webui/dist

if [[ ! -f /etc/bili-scroll/bili-scroll.env ]]; then
  install -m 0640 -g bili-scroll deploy/env.example /etc/bili-scroll/bili-scroll.env
  KEY="$(openssl rand -base64 32)"
  sed -i "s|REPLACE_WITH_BASE64_32_BYTES|${KEY}|" /etc/bili-scroll/bili-scroll.env
  sed -i "s|https://YOUR-DOMAIN|https://${DOMAIN}|" /etc/bili-scroll/bili-scroll.env
fi

set -a
# shellcheck disable=SC1091
source /etc/bili-scroll/bili-scroll.env
set +a

install -m 0644 deploy/bili-scroll.service /etc/systemd/system/bili-scroll.service
systemctl daemon-reload
systemctl enable --now bili-scroll

sed "s/YOUR-DOMAIN/${DOMAIN}/g" deploy/Caddyfile > /etc/caddy/Caddyfile.bili-scroll
if command -v caddy >/dev/null 2>&1; then
  caddy validate --config /etc/caddy/Caddyfile.bili-scroll --adapter caddyfile
fi
for _ in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:8788/api/health >/dev/null; then
    break
  fi
  sleep 1
done
curl -fsS http://127.0.0.1:8788/api/health
echo "App started. Include /etc/caddy/Caddyfile.bili-scroll in Caddy, validate, then reload Caddy."
echo "Create the first admin:"
echo "  cd /opt/bili-scroll && runuser -u bili-scroll -- bin/bili-scroll-server create-admin"

