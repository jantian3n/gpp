#!/usr/bin/env bash
# gpp 服务端一键安装脚本（Debian / Ubuntu）
# 默认安装 hysteria2 协议（UDP/QUIC，适合游戏加速；弱网抗丢包）
#
# 用法:
#   curl -fsSL https://raw.githubusercontent.com/jantian3n/gpp/main/server/install-debian.sh | bash
# 或本地下载后:
#   bash install-debian.sh [--protocol hysteria2] [--port 5123] [--addr 0.0.0.0] \
#                          [--name vps] [--path /usr/local/gpp] [--net-addr IP:端口]
#
# 说明:
#   --protocol  vless | shadowsocks | socks | hysteria2   (默认 hysteria2)
#   --port      监听端口 (默认 5123)
#   --addr      监听地址 (默认 0.0.0.0)
#   --name      客户端里显示的节点名称 (默认 vps)
#   --path      安装目录 (默认 /usr/local/gpp)
#   --net-addr  客户端入口地址，有中转填中转地址 (默认自动探测 公网IP:端口)
#
# 脚本可重复执行（幂等）：已存在的 config.json 和 gpp 二进制不会覆盖，
# 重复执行只用于重装服务/补开端口，并重新打印导入链接。
set -euo pipefail

log()  { echo -e "\033[32m[+] $*\033[0m"; }
warn() { echo -e "\033[33m[!] $*\033[0m"; }
err()  { echo -e "\033[31m[x] $*\033[0m" >&2; }

if [ "$(id -u)" -ne 0 ]; then
    err "请使用 root 运行本脚本 (sudo bash install-debian.sh)"
    exit 1
fi

# ---------- 参数 ----------
PROTOCOL="hysteria2"
PORT="5123"
ADDR="0.0.0.0"
NAME="vps"
INSTALL_PATH="/usr/local/gpp"
NET_ADDR=""

while [ $# -gt 0 ]; do
    case "$1" in
        --protocol) PROTOCOL="$2"; shift 2 ;;
        --port)     PORT="$2";     shift 2 ;;
        --addr)     ADDR="$2";     shift 2 ;;
        --name)     NAME="$2";     shift 2 ;;
        --path)     INSTALL_PATH="$2"; shift 2 ;;
        --net-addr) NET_ADDR="$2"; shift 2 ;;
        -h|--help)  grep '^#' "$0" | head -20; exit 0 ;;
        *) err "未知参数: $1 (用 --help 查看用法)"; exit 1 ;;
    esac
done

case "$PROTOCOL" in
    vless|shadowsocks|socks|hysteria2) ;;
    *) err "无效协议: $PROTOCOL (可选 vless/shadowsocks/socks/hysteria2)"; exit 1 ;;
esac

# ---------- 依赖 ----------
NEED_PKGS=()
command -v curl >/dev/null 2>&1 || NEED_PKGS+=(curl)
command -v tar  >/dev/null 2>&1 || NEED_PKGS+=(tar)
if [ "${#NEED_PKGS[@]}" -gt 0 ]; then
    log "安装依赖: ${NEED_PKGS[*]}"
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${NEED_PKGS[@]}"
fi

# ---------- 架构 ----------
case "$(uname -m)" in
    x86_64)        ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) err "不支持的架构: $(uname -m)"; exit 1 ;;
esac
log "系统架构: ${ARCH}"

mkdir -p "$INSTALL_PATH"
cd "$INSTALL_PATH"

# ---------- 下载服务端 ----------
if [ -f gpp ]; then
    log "已存在 gpp 二进制，跳过下载"
else
    FILE="gpp-linux-${ARCH}.tar.gz"

    # 从 fork 的最新 release 解析资产地址（checksums 文件名带版本号，无法硬编码）。
    # 解析不到就拒绝安装（fail closed）：宁可装不上，也不跑来源不明的二进制。
    log "查询最新 release 资产..."
    RELEASE_JSON=$(curl -fsSL --connect-timeout 10 --retry 2 \
        https://api.github.com/repos/jantian3n/gpp/releases/latest \
        || { err "无法访问 GitHub API，无法获取校验信息，已中止安装"; exit 1; })
    CHECKSUMS_URL=$(printf '%s' "$RELEASE_JSON" \
        | grep '"browser_download_url"' | grep 'checksums\.txt' \
        | sed 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/' \
        | head -n 1 || true)
    ASSET_URL=$(printf '%s' "$RELEASE_JSON" \
        | grep '"browser_download_url"' | grep "${FILE}" \
        | sed 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/' \
        | head -n 1 || true)
    [ -n "$CHECKSUMS_URL" ] || { err "未在最新 release 中找到 checksums 文件，已中止安装"; exit 1; }
    [ -n "$ASSET_URL" ] || { err "未在最新 release 中找到 ${FILE}，已中止安装"; exit 1; }

    # 校验文件很小，直接从解析出的 release 资产地址下载，不走镜像代理
    CHECKSUMS_FILE="${CHECKSUMS_URL##*/}"
    log "下载校验文件: ${CHECKSUMS_URL}"
    curl -fsSL --connect-timeout 10 --retry 2 -o "$CHECKSUMS_FILE" "$CHECKSUMS_URL" \
        || { err "下载校验文件失败，已中止安装"; exit 1; }

    # 二进制本体较大：直连失败时用镜像代理（仅替换 github.com 域名前缀）作备选
    URLS=("$ASSET_URL")
    for MIRROR in ghproxy.net gh-proxy.com ghfast.top; do
        URLS+=("$(printf '%s' "$ASSET_URL" \
            | sed "s|^https://github.com/|https://${MIRROR}/https://github.com/|")")
    done
    ok=""
    for url in "${URLS[@]}"; do
        log "下载服务端: ${url}"
        if curl -fL -o "$FILE" --connect-timeout 10 --retry 1 "$url"; then ok=1; break; fi
        warn "下载失败，尝试下一个源..."
    done
    [ -n "$ok" ] || { err "所有下载源均失败，请检查网络或将 gpp 二进制手动放到 ${INSTALL_PATH}"; rm -f "$CHECKSUMS_FILE"; exit 1; }

    # sha256 严格校验：不匹配立即报错退出（fail closed），绝不执行被篡改的二进制
    log "校验 ${FILE} 的 sha256..."
    if ! grep " ${FILE}$" "$CHECKSUMS_FILE" | sha256sum -c -; then
        err "校验失败：${FILE} 与 ${CHECKSUMS_FILE} 记录不符，已拒绝安装（谨防下载源被投毒）"
        rm -f "$FILE" "$CHECKSUMS_FILE"
        exit 1
    fi

    tar -xzf "$FILE" gpp-server
    mv gpp-server gpp
    rm -f "$FILE" "$CHECKSUMS_FILE"
    chmod +x gpp
fi

# ---------- 配置 ----------
if [ -f config.json ]; then
    log "已存在 config.json，保留原配置"
    PROTOCOL=$(sed -n 's/.*"protocol"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' config.json | head -1)
    PORT=$(sed -n 's/.*"port"[[:space:]]*:[[:space:]]*\([0-9]*\).*/\1/p' config.json | head -1)
    UUID=$(sed -n 's/.*"uuid"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' config.json | head -1)
    log "读取到配置: protocol=${PROTOCOL} port=${PORT}"
else
    UUID=$(cat /proc/sys/kernel/random/uuid)
    cat > config.json <<EOF
{
  "protocol": "${PROTOCOL}",
  "port": ${PORT},
  "addr": "${ADDR}",
  "uuid": "${UUID}"
}
EOF
    log "已生成 config.json (uuid: ${UUID})"
fi

# ---------- 入口地址 ----------
if [ -z "$NET_ADDR" ]; then
    PUBIP=$(curl -fsS4 --max-time 10 https://ipv4.ip.sb 2>/dev/null \
         || curl -fsS4 --max-time 10 https://ifconfig.me 2>/dev/null \
         || curl -fsS4 --max-time 10 https://api.ip.sb/ip 2>/dev/null || true)
    if [ -z "$PUBIP" ]; then
        err "无法自动探测公网 IP，请用 --net-addr IP:端口 重新运行"
        exit 1
    fi
    NET_ADDR="${PUBIP}:${PORT}"
fi
log "客户端入口地址: ${NET_ADDR}"

# ---------- 防火墙 ----------
open_port() {
    local proto=$1
    if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
        ufw allow "${PORT}/${proto}" >/dev/null 2>&1 && log "ufw 已放行 ${PORT}/${proto}"
    elif command -v iptables >/dev/null 2>&1; then
        if ! iptables -C INPUT -p "${proto}" --dport "${PORT}" -j ACCEPT 2>/dev/null; then
            iptables -I INPUT -p "${proto}" --dport "${PORT}" -j ACCEPT 2>/dev/null \
                && log "iptables 已放行 ${PORT}/${proto}" || true
        fi
    fi
}
open_port tcp
open_port udp
if [ "$PROTOCOL" = "hysteria2" ]; then
    warn "hysteria2 走 UDP，请确认云服务商网页防火墙/安全组也放行了 UDP ${PORT}"
fi

# ---------- BBR (对 TCP 类协议有效，hysteria2 无效但无害) ----------
if ! sysctl net.ipv4.tcp_congestion_control 2>/dev/null | grep -q bbr; then
    modprobe tcp_bbr 2>/dev/null || true
    cat > /etc/sysctl.d/99-gpp-bbr.conf <<EOF
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
EOF
    sysctl -p /etc/sysctl.d/99-gpp-bbr.conf >/dev/null 2>&1 \
        && log "已启用 BBR" || warn "BBR 启用失败（内核不支持，可忽略）"
fi

# ---------- systemd 服务 ----------
cat > /etc/systemd/system/gpp.service <<EOF
[Unit]
Description=gpp proxy server
After=network.target

[Service]
Type=simple
WorkingDirectory=${INSTALL_PATH}
ExecStart=${INSTALL_PATH}/gpp
Restart=always
RestartSec=3
LimitNOFILE=1000000

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now gpp >/dev/null 2>&1 || systemctl restart gpp
sleep 2

if systemctl is-active --quiet gpp; then
    log "服务运行中: $(systemctl is-active gpp)"
else
    err "服务启动失败，排查: journalctl -u gpp -e  或查看 ${INSTALL_PATH}/run.log"
    exit 1
fi
ss -tlnp 2>/dev/null | grep -q ":${PORT}" && log "TCP ${PORT} 监听正常" || warn "未发现 TCP ${PORT} 监听（hysteria2 只有 UDP 属正常现象）"
ss -ulnp 2>/dev/null | grep -q ":${PORT}" && log "UDP ${PORT} 监听正常" || true

# ---------- 导入链接 ----------
LINK=$(echo -n "gpp://${PROTOCOL}@${NET_ADDR}/${UUID}" | base64 -w0)
echo
log "========== 安装完成 =========="
echo -e "  节点导入链接: \033[36m${LINK}#${NAME}\033[0m"
echo    "  在客户端节点列表窗口粘贴即可导入"
echo    "  管理: systemctl status|restart|stop gpp"
echo    "  日志: ${INSTALL_PATH}/run.log"
echo    "  配置: ${INSTALL_PATH}/config.json"
log "================================"
