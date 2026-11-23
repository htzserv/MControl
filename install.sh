#!/usr/bin/env bash
# MControl v2 — MDesign Installer (Static IP-Only Header + Stable & Beta Channels)
MODULE_VERSION="1.3.4"
set -euo pipefail
umask 077

B='\033[1;34m'
G='\033[1;32m'
Y='\033[1;33m'
R='\033[1;31m'
C='\033[0;36m'
M='\033[1;35m'
W='\033[1;37m'
DIM='\033[2;37m'
NC='\033[0m'
FR='\033[1;33m' # Gold frame matching MTunnel

REPO="${MCONTROL_REPO:-htzserv/MControl}"
CHANNEL="${MCONTROL_CHANNEL:-stable}"
PORT="${MCONTROL_PORT:-2053}"
NO_LAUNCH=0
DO_UNINSTALL=0
BIN="/usr/local/bin/mcontrol"
MENU_BIN="/usr/local/bin/mcontrol-menu"
DATA_DIR="/var/lib/mcontrol"
LOCAL_DIR="/root/mcontrol"
SERVICE="mcontrol"

usage() {
    echo "Usage: sudo bash install.sh [--stable | --beta] [--port PORT] [--no-launch] [--uninstall]"
    echo "  --stable       Install Stable channel (v2-latest) [default]"
    echo "  --beta         Install Beta channel (v2-beta)"
    echo "  --port PORT    Set panel listen port (default: 2053)"
    echo "  --no-launch    Install without opening mcontrol-menu afterwards"
    echo "  --uninstall    Remove MControl completely"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --beta)      CHANNEL="beta"; shift ;;
        --stable)    CHANNEL="stable"; shift ;;
        --port)      PORT="${2:?--port requires a number}"; shift 2 ;;
        --no-launch) NO_LAUNCH=1; shift ;;
        --uninstall) DO_UNINSTALL=1; shift ;;
        -h|--help)   usage; exit 0 ;;
        *)           echo -e "  ${R}✖ Unknown option: $1${NC}" >&2; usage; exit 1 ;;
    esac
done

if [[ "$(id -u)" -ne 0 ]]; then
    echo -e "  ${R}✖ Please run as root: sudo bash install.sh${NC}" >&2
    exit 1
fi

if [[ "$DO_UNINSTALL" -eq 1 ]]; then
    systemctl disable --now "$SERVICE" 2>/dev/null || true
    rm -f "/etc/systemd/system/${SERVICE}.service" "$BIN" "$MENU_BIN"
    systemctl daemon-reload 2>/dev/null || true
    rm -rf "$DATA_DIR" "$LOCAL_DIR"
    echo -e "  ${G}✓ MControl uninstalled.${NC}"
    exit 0
fi

# ── Detect Static IPs ────────────────────────────────────────────────────────
IP4=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}' || true)
[[ -n "${IP4:-}" ]] || IP4=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
[[ -n "${IP4:-}" ]] || IP4="N/A"
IP6=$(ip -6 addr show scope global 2>/dev/null | awk '/inet6/{split($2,a,"/"); if(a[1] !~ /^f[cd]/){print a[1]; exit}}' || true)
[[ -n "${IP6:-}" ]] || IP6="OFF"

TAG="v2-latest"
CH_LABEL="STABLE (v2-latest)"
CH_COL="$G"
if [[ "$CHANNEL" == "beta" ]]; then
    TAG="v2-beta"
    CH_LABEL="BETA (v2-beta)"
    CH_COL="$Y"
fi

# ── Draw MDesign Gold Static Header (IPs Only) ──────────────────────────────
clear 2>/dev/null || true
BOXW=66
printf -v BORDER '%*s' "$((BOXW - 2))" ''
BORDER="${BORDER// /═}"

hdr_line() {
    local plain="$1" colored="$2" w=$((BOXW - 4)) LC_CTYPE=C.UTF-8
    (( ${#plain} > w )) && { plain="${plain:0:$((w-1))}…"; colored="$plain"; }
    printf "  ${FR}║${NC} %b%*s ${FR}║${NC}\n" "$colored" "$((w - ${#plain}))" ""
}

V6_COL="$M"; [[ "$IP6" == "OFF" ]] && V6_COL="$DIM"
echo ""
printf "  ${FR}╔%s╗${NC}\n" "$BORDER"
hdr_line "MControl v2 Installer  v${MODULE_VERSION}  |  ${CH_LABEL}" "${W}MControl v2 Installer${NC}  ${C}v${MODULE_VERSION}${NC}  ${DIM}|${NC}  ${CH_COL}${CH_LABEL}${NC}"
hdr_line "IPv4: ${IP4}  |  Port: ${PORT}" "${DIM}IPv4:${NC} ${M}${IP4}${NC}  ${DIM}|  Port:${NC} ${C}${PORT}${NC}"
hdr_line "IPv6: ${IP6}" "${DIM}IPv6:${NC} ${V6_COL}${IP6}${NC}"
printf "  ${FR}╚%s╝${NC}\n\n" "$BORDER"

echo -e "  ${FR}┌─[ INSTALLATION ]${NC}"
echo -e "  ${FR}│${NC}"

# 1. Prerequisites
if ! command -v curl >/dev/null 2>&1; then
    echo -e "  ${FR}├─${NC} ${C}Installing curl & ca-certificates...${NC}"
    if command -v apt-get >/dev/null 2>&1; then
        apt-get update -y >/dev/null && apt-get install -y curl ca-certificates >/dev/null
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y curl ca-certificates >/dev/null
    elif command -v yum >/dev/null 2>&1; then
        yum install -y curl ca-certificates >/dev/null
    fi
fi

# 2. Architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64)  GOARCH="amd64" ;;
    aarch64|arm64) GOARCH="arm64" ;;
    *) echo -e "  ${FR}└─${NC} ${R}✖ Unsupported architecture: ${ARCH}${NC}"; exit 1 ;;
esac

mc_dl() {
    local url="$1" dest="$2"
    curl -fsSL --connect-timeout 8 --max-time 60 "$url" -o "$dest" 2>/dev/null \
        || curl -fsSL --connect-timeout 10 --max-time 60 "https://gh-proxy.com/${url}" -o "$dest" 2>/dev/null
}

BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"
echo -e "  ${FR}├─${NC} ${C}Downloading MControl binary (${GOARCH} · ${TAG})...${NC}"
if ! mc_dl "${BASE_URL}/mcontrol-linux-${GOARCH}" "${BIN}.tmp"; then
    echo -e "  ${FR}└─${NC} ${R}✖ Download failed from GitHub & Mirror.${NC}"
    rm -f "${BIN}.tmp"
    exit 1
fi

if mc_dl "${BASE_URL}/mcontrol-linux-${GOARCH}.sha256" "/tmp/mcontrol.sha256"; then
    EXP=$(awk '{print $1}' /tmp/mcontrol.sha256 | tr -d '[:space:]')
    GOT=$(sha256sum "${BIN}.tmp" | awk '{print $1}')
    rm -f /tmp/mcontrol.sha256
    if [[ -n "$EXP" && "${EXP,,}" != "${GOT,,}" ]]; then
        echo -e "  ${FR}└─${NC} ${R}✖ SHA-256 verification failed!${NC}"
        rm -f "${BIN}.tmp"
        exit 1
    fi
    echo -e "  ${FR}├─${NC} ${G}✓ SHA-256 verified.${NC}"
fi

chmod +x "${BIN}.tmp"
mv -f "${BIN}.tmp" "$BIN"

# 3. Install Terminal Menu (main.sh -> mcontrol-menu)
mkdir -p "$DATA_DIR" "$LOCAL_DIR"
chmod 700 "$DATA_DIR"
printf '%s\n' "$CHANNEL" > "${DATA_DIR}/.channel"

SCRIPT_DIR=""
if [[ -f "${BASH_SOURCE[0]:-}" ]]; then
    SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P || true)
fi

if [[ -n "$SCRIPT_DIR" && -f "${SCRIPT_DIR}/main.sh" ]]; then
    cp -f "${SCRIPT_DIR}/main.sh" "${LOCAL_DIR}/main.sh"
elif mc_dl "${BASE_URL}/main.sh" "${LOCAL_DIR}/main.sh"; then
    true
else
    mc_dl "https://raw.githubusercontent.com/${REPO}/main/main.sh" "${LOCAL_DIR}/main.sh" || true
fi

if [[ -f "${LOCAL_DIR}/main.sh" ]]; then
    chmod +x "${LOCAL_DIR}/main.sh"
    cp -f "${LOCAL_DIR}/main.sh" "$MENU_BIN"
    chmod +x "$MENU_BIN"
    echo -e "  ${FR}├─${NC} ${G}✓ Terminal menu installed (${W}mcontrol-menu${G}).${NC}"
fi

# 4. Systemd Service
cat > "/etc/systemd/system/${SERVICE}.service" <<EOF
[Unit]
Description=MControl v2 Multi-Tenant Panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${BIN} -port ${PORT} -data ${DATA_DIR}
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE" >/dev/null 2>&1
systemctl restart "$SERVICE"

echo -e "  ${FR}├─${NC} ${G}✓ Systemd service active.${NC}"
echo -e "  ${FR}│${NC}"
echo -e "  ${FR}├─${NC} ${W}Web Panel :${NC} ${C}http://${IP4}:${PORT}${NC}"
echo -e "  ${FR}├─${NC} ${W}CLI Menu  :${NC} ${Y}mcontrol-menu${NC}"
echo -e "  ${FR}└─${NC} ${W}Channel   :${NC} ${CH_COL}${CH_LABEL}${NC}\n"

if [[ "$NO_LAUNCH" -eq 0 && -x "$MENU_BIN" && -t 0 && -t 1 ]]; then
    exec "$MENU_BIN"
fi
