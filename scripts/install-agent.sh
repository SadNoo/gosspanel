#!/usr/bin/env bash
set -euo pipefail

REPO="${GOSS_REPO:-SadNoo/gosspanel}"
VERSION="${GOSS_VERSION:-latest}"
GOST_VERSION="${GOST_VERSION:-v3.2.6}"
INSTALL_DIR="${GOSS_INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="${GOSS_CONFIG_DIR:-/etc/goss}"
ENV_FILE="${CONFIG_DIR}/agent.env"

ROLE=""
SERVER=""
TOKEN=""
NODE_ID=""
NAME=""
REGION=""
INTERVAL="5s"
REPORT_IP=""
INSTALL_GOST=1
INSECURE_TLS=0

usage() {
  cat <<EOF
用法:
  install-agent.sh --role relay|client --server http://面板IP:8080 --token TOKEN [选项]

选项:
  --node-id ID       节点 ID，默认使用 hostname-role
  --name NAME        面板显示名称
  --region REGION    区域名称
  --interval 5s      心跳间隔
  --report-ip IP     client 上报 IP，client 默认自动取本机首个 IP
  --insecure-tls     跳过面板 TLS 证书校验，仅用于无域名自签 HTTPS 面板
  --no-gost          不安装 GOST
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --role) ROLE="${2:-}"; shift 2 ;;
    --server) SERVER="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --node-id) NODE_ID="${2:-}"; shift 2 ;;
    --name) NAME="${2:-}"; shift 2 ;;
    --region) REGION="${2:-}"; shift 2 ;;
    --interval) INTERVAL="${2:-}"; shift 2 ;;
    --report-ip) REPORT_IP="${2:-}"; shift 2 ;;
    --insecure-tls) INSECURE_TLS=1; shift ;;
    --no-gost) INSTALL_GOST=0; shift ;;
    -h | --help) usage; exit 0 ;;
    *) echo "未知参数: $1"; usage; exit 1 ;;
  esac
done

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

install_gost() {
  if [ "$INSTALL_GOST" != "1" ]; then
    return
  fi
  if command -v gost >/dev/null 2>&1; then
    gost -V
    return
  fi
  local arch tmp url
  arch="$(detect_arch)"
  tmp="$(mktemp -d)"
  url="https://github.com/go-gost/gost/releases/download/${GOST_VERSION}/gost_${GOST_VERSION#v}_linux_${arch}.tar.gz"
  echo "下载 GOST: $url"
  mkdir -p "$INSTALL_DIR"
  curl -fL "$url" -o "$tmp/gost.tar.gz"
  tar -xzf "$tmp/gost.tar.gz" -C "$tmp"
  install -m 755 "$tmp/gost" "${INSTALL_DIR}/gost"
  rm -rf "$tmp"
}

auto_report_ip() {
  hostname -I 2>/dev/null | awk '{print $1}'
}

validate_args() {
  if [ "$ROLE" != "relay" ] && [ "$ROLE" != "client" ]; then
    echo "--role 必须是 relay 或 client"
    exit 1
  fi
  if [ -z "$SERVER" ] || [ -z "$TOKEN" ]; then
    echo "--server 和 --token 必填"
    exit 1
  fi
  if [ -z "$NODE_ID" ]; then
    NODE_ID="$(hostname)-${ROLE}"
  fi
  if [ -z "$NAME" ]; then
    if [ "$ROLE" = "relay" ]; then
      NAME="Goss Relay $(hostname)"
    else
      NAME="Goss Client $(hostname)"
    fi
  fi
  if [ -z "$REGION" ]; then
    REGION="$ROLE"
  fi
  if [ "$ROLE" = "client" ] && [ -z "$REPORT_IP" ]; then
    REPORT_IP="$(auto_report_ip || true)"
  fi
}

write_env() {
  mkdir -p "$CONFIG_DIR"
  chmod 700 "$CONFIG_DIR"
  cat >"$ENV_FILE" <<EOF
GOSS_AGENT_TOKEN=${TOKEN}
EOF
  chmod 600 "$ENV_FILE"
}

write_service() {
  local service extra
  service="goss-${ROLE}-agent"
  extra=""
  if [ "$INSECURE_TLS" = "1" ]; then
    extra="${extra} -insecure-tls"
  fi
  if [ "$ROLE" = "client" ] && [ -n "$REPORT_IP" ]; then
    extra="${extra} -report-ip ${REPORT_IP}"
  fi
  cat >"/etc/systemd/system/${service}.service" <<EOF
[Unit]
Description=goss ${ROLE} agent
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=${ENV_FILE}
ExecStart=/bin/sh -c 'exec ${INSTALL_DIR}/goss agent -server "${SERVER}" -token "\$GOSS_AGENT_TOKEN" -role ${ROLE} -node-id "${NODE_ID}" -name "${NAME}" -region "${REGION}" -interval ${INTERVAL}${extra}'
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now "${service}.service"
  systemctl status "${service}.service" --no-pager -l
}

need_root
validate_args
command -v curl >/dev/null || { echo "缺少 curl"; exit 1; }
command -v tar >/dev/null || { echo "缺少 tar"; exit 1; }
download_goss
install_gost
write_env
write_service
