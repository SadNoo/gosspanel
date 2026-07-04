#!/usr/bin/env bash
set -euo pipefail

REPO="${GOSS_REPO:-SadNoo/gosspanel}"
VERSION="${GOSS_VERSION:-latest}"
INSTALL_DIR="${GOSS_INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="${GOSS_CONFIG_DIR:-/etc/goss}"
DATA_DIR="${GOSS_DATA_DIR:-/var/lib/goss}"
SERVICE_FILE="/etc/systemd/system/goss-panel.service"
ENV_FILE="${CONFIG_DIR}/panel.env"

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

write_env() {
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
  admin_password="${GOSS_ADMIN_PASSWORD:-$(random_hex 12)}"
  session_secret="${GOSS_SESSION_SECRET:-$(random_hex 32)}"
  agent_token="${GOSS_AGENT_TOKEN:-$(random_hex 32)}"

  cat >"$ENV_FILE" <<EOF
GOSS_ADDR=${GOSS_ADDR:-:8080}
GOSS_DATA=${GOSS_DATA:-${DATA_DIR}/goss.db}
GOSS_ADMIN_USER=${admin_user}
GOSS_ADMIN_PASSWORD=${admin_password}
GOSS_SESSION_SECRET=${session_secret}
GOSS_AGENT_TOKEN=${agent_token}
EOF
  chmod 600 "$ENV_FILE"

  echo "面板初始用户名: ${admin_user}"
  echo "面板初始密码: ${admin_password}"
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
  systemctl enable --now goss-panel.service
}

need_root
command -v curl >/dev/null || { echo "缺少 curl"; exit 1; }
command -v tar >/dev/null || { echo "缺少 tar"; exit 1; }
download_goss
write_env
write_service
systemctl status goss-panel.service --no-pager -l
