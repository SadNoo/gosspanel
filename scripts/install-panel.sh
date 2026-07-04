#!/usr/bin/env bash
set -euo pipefail

REPO="${GOSS_REPO:-SadNoo/gosspanel}"
VERSION="${GOSS_VERSION:-latest}"
INSTALL_DIR="${GOSS_INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="${GOSS_CONFIG_DIR:-/etc/goss}"
DATA_DIR="${GOSS_DATA_DIR:-/var/lib/goss}"
SERVICE_FILE="/etc/systemd/system/goss-panel.service"
ENV_FILE="${CONFIG_DIR}/panel.env"
CADDY_FILE="/etc/caddy/Caddyfile"
TLS_CERT_FILE="/etc/caddy/goss-panel.crt"
TLS_KEY_FILE="/etc/caddy/goss-panel.key"

need_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "请使用 root 执行安装脚本"
    exit 1
  fi
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo "amd64" ;;
    aarch64 | arm64) echo "arm64" ;;
    *) echo "不支持的架构: $(uname -m)" >&2; exit 1 ;;
  esac
}

random_hex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$1"
  else
    od -An -N "$1" -tx1 /dev/urandom | tr -d ' \n'
  fi
}

asset_url() {
  local arch="$1"
  if [ -n "${GOSS_BINARY_URL:-}" ]; then
    echo "$GOSS_BINARY_URL"
    return
  fi
  if [ "$VERSION" = "latest" ]; then
    echo "https://github.com/${REPO}/releases/latest/download/goss_linux_${arch}.tar.gz"
  else
    echo "https://github.com/${REPO}/releases/download/${VERSION}/goss_linux_${arch}.tar.gz"
  fi
}

download_goss() {
  local arch tmp url
  arch="$(detect_arch)"
  tmp="$(mktemp -d)"
  url="$(asset_url "$arch")"
  echo "下载 goss: $url"
  mkdir -p "$INSTALL_DIR"
  curl -fL "$url" -o "$tmp/goss.tar.gz"
  tar -xzf "$tmp/goss.tar.gz" -C "$tmp"
  install -m 755 "$tmp/goss" "${INSTALL_DIR}/goss"
  rm -rf "$tmp"
}

https_enabled() {
  [ "${GOSS_HTTPS:-}" = "1" ] || [ -n "${GOSS_DOMAIN:-}" ]
}

normalize_domain() {
  local domain
  domain="${GOSS_DOMAIN:-}"
  domain="${domain#http://}"
  domain="${domain#https://}"
  domain="${domain%%/*}"
  if [ -n "$domain" ] && [[ ! "$domain" =~ ^[A-Za-z0-9.-]+$ ]]; then
    echo "GOSS_DOMAIN 格式不正确: ${GOSS_DOMAIN}" >&2
    exit 1
  fi
  echo "$domain"
}

install_caddy() {
  if command -v caddy >/dev/null 2>&1; then
    return
  fi

  if command -v apt-get >/dev/null 2>&1; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg
    rm -f /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/gpg.key \
      | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt \
      -o /etc/apt/sources.list.d/caddy-stable.list
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y caddy
    return
  fi

  echo "未找到 caddy，且当前系统不支持自动安装。请先安装 caddy 后重试。"
  exit 1
}

write_caddyfile() {
  local domain upstream
  domain="$(normalize_domain)"
  upstream="${GOSS_ADDR:-127.0.0.1:8080}"
  mkdir -p /etc/caddy

  if [ -n "$domain" ]; then
    cat >"$CADDY_FILE" <<EOF
${domain} {
  encode zstd gzip
  reverse_proxy ${upstream}
}
EOF
  else
    write_self_signed_cert
    cat >"$CADDY_FILE" <<EOF
:443 {
  tls ${TLS_CERT_FILE} ${TLS_KEY_FILE}
  encode zstd gzip
  reverse_proxy ${upstream}
}
EOF
  fi

  caddy fmt --overwrite "$CADDY_FILE"
  systemctl enable --now caddy
  systemctl reload caddy || systemctl restart caddy
}

detect_primary_ip() {
  if [ -n "${GOSS_TLS_IP:-}" ]; then
    echo "$GOSS_TLS_IP"
    return
  fi
  hostname -I 2>/dev/null | awk '{print $1}'
}

write_self_signed_cert() {
  local ip san
  command -v openssl >/dev/null || { echo "缺少 openssl，无法生成无域名 HTTPS 证书"; exit 1; }
  mkdir -p /etc/caddy
  ip="$(detect_primary_ip || true)"
  san="IP:127.0.0.1,DNS:localhost"
  if [ -n "$ip" ]; then
    san="IP:${ip},${san}"
  else
    ip="localhost"
  fi
  openssl req -x509 -nodes -newkey rsa:2048 \
    -days "${GOSS_TLS_DAYS:-3650}" \
    -keyout "$TLS_KEY_FILE" \
    -out "$TLS_CERT_FILE" \
    -subj "/CN=${ip}" \
    -addext "subjectAltName=${san}" >/dev/null 2>&1
  chmod 640 "$TLS_KEY_FILE"
  chmod 644 "$TLS_CERT_FILE"
  chown root:caddy "$TLS_KEY_FILE" "$TLS_CERT_FILE" 2>/dev/null || true
}

write_env() {
  local requested_addr generated_password
  requested_addr="${GOSS_ADDR:-}"
  generated_password=0

  mkdir -p "$CONFIG_DIR" "$DATA_DIR"
  chmod 700 "$CONFIG_DIR" "$DATA_DIR"
  if [ -f "$ENV_FILE" ]; then
    set -a
    # shellcheck disable=SC1090
    . "$ENV_FILE"
    set +a
  fi

  local admin_user admin_password session_secret agent_token
  admin_user="${GOSS_ADMIN_USER:-admin}"
  if [ -n "${GOSS_ADMIN_PASSWORD:-}" ]; then
    admin_password="${GOSS_ADMIN_PASSWORD}"
  else
    admin_password="$(random_hex 12)"
    generated_password=1
  fi
  session_secret="${GOSS_SESSION_SECRET:-$(random_hex 32)}"
  agent_token="${GOSS_AGENT_TOKEN:-$(random_hex 32)}"

  if https_enabled; then
    GOSS_ADDR="${requested_addr:-127.0.0.1:8080}"
  else
    GOSS_ADDR="${GOSS_ADDR:-:8080}"
  fi

  cat >"$ENV_FILE" <<EOF
GOSS_ADDR=${GOSS_ADDR}
GOSS_DATA=${GOSS_DATA:-${DATA_DIR}/goss.db}
GOSS_ADMIN_USER=${admin_user}
GOSS_ADMIN_PASSWORD=${admin_password}
GOSS_SESSION_SECRET=${session_secret}
GOSS_AGENT_TOKEN=${agent_token}
EOF
  chmod 600 "$ENV_FILE"

  echo "面板用户名: ${admin_user}"
  if [ "$generated_password" = "1" ]; then
    echo "面板初始密码: ${admin_password}"
  else
    echo "面板密码: 已保留现有配置"
  fi
}

write_service() {
  cat >"$SERVICE_FILE" <<EOF
[Unit]
Description=goss panel
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_DIR}/goss
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable goss-panel.service
  systemctl restart goss-panel.service
}

need_root
command -v curl >/dev/null || { echo "缺少 curl"; exit 1; }
command -v tar >/dev/null || { echo "缺少 tar"; exit 1; }
if [ "${GOSS_SKIP_DOWNLOAD:-}" != "1" ]; then
  download_goss
fi
write_env
write_service
if https_enabled; then
  install_caddy
  write_caddyfile
fi
systemctl status goss-panel.service --no-pager -l
if https_enabled; then
  systemctl status caddy --no-pager -l
fi
