#!/usr/bin/env bash
# MControl v1.4.0 — MTunnel-Style MDesign Installer (Static IP-Only Header + Stable/Beta)
MODULE_VERSION="1.4.4"
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
FR='\033[1;33m'

if [[ "$(id -u)" -ne 0 ]]; then
    echo -e "  ${R}✖ Run as root: sudo bash install.sh [--stable|--beta] [port]${NC}" >&2
    exit 1
fi

PORT="2053"
CHANNEL="${MCONTROL_CHANNEL:-stable}"
DO_WIPE=0
for arg in "$@"; do
    case "$arg" in
        --beta|beta)     CHANNEL="beta" ;;
    --wipe|wipe)     DO_WIPE=1 ;;
        --stable|stable) CHANNEL="stable" ;;
        [0-9]*)          PORT="$arg" ;;
    esac
done

INSTALL_DIR="/opt/mcontrol"
DATA_DIR="/var/lib/mcontrol"
REPO="${MCONTROL_REPO:-htzserv/MControl}"
if [[ -n "${MCONTROL_RELEASE_BASE:-}" ]]; then
    RELEASE_BASE="$MCONTROL_RELEASE_BASE"
elif [[ "$CHANNEL" == "beta" ]]; then
    RELEASE_BASE="https://github.com/${REPO}/releases/download/beta-latest"
else
    RELEASE_BASE="https://github.com/${REPO}/releases/latest/download"
fi

case "$(uname -m)" in
    aarch64|arm64) ARCH="arm64" ;;
    *)             ARCH="amd64" ;;
esac

if [[ "$DO_WIPE" -eq 1 ]]; then
    echo -e "  ${Y}● Wiping all previous MControl binaries, services & databases...${NC}"
    systemctl disable --now mcontrol.service 2>/dev/null || true
    rm -f /etc/systemd/system/mcontrol.service /usr/local/bin/mcontrol /usr/local/bin/mcontrol-menu
    rm -rf /opt/mcontrol /var/lib/mcontrol /root/mcontrol /etc/mcontrol
    systemctl daemon-reload 2>/dev/null || true
    echo -e "  ${G}✓ Previous installation and databases completely wiped.${NC}"
fi

# ── Static IP Detection & Gold Header (No Live Refresh Loop) ────────────────
IP4=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}' || true)
[[ -n "${IP4:-}" ]] || IP4=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
[[ -n "${IP4:-}" ]] || IP4="127.0.0.1"
IP6=$(ip -6 addr show scope global 2>/dev/null | awk '/inet6/{split($2,a,"/"); if(a[1] !~ /^f[cd]/){print a[1]; exit}}' || true)
[[ -n "${IP6:-}" ]] || IP6="OFF"

CH_LBL="STABLE (latest)"; CH_COL="$G"
[[ "$CHANNEL" == "beta" ]] && { CH_LBL="BETA (beta-latest)"; CH_COL="$Y"; }
V6_COL="$M"; [[ "$IP6" == "OFF" ]] && V6_COL="$DIM"

clear 2>/dev/null || true
BOXW=66
printf -v BORDER '%*s' "$((BOXW - 2))" ''
BORDER="${BORDER// /═}"
hdr_line() {
    local plain="$1" colored="$2" w=$((BOXW - 4)) LC_CTYPE=C.UTF-8
    (( ${#plain} > w )) && { plain="${plain:0:$((w-1))}…"; colored="$plain"; }
    printf "  ${FR}║${NC} %b%*s ${FR}║${NC}\n" "$colored" "$((w - ${#plain}))" ""
}

echo ""
printf "  ${FR}╔%s╗${NC}\n" "$BORDER"
hdr_line "MControl Installer  v${MODULE_VERSION}  |  ${CH_LBL}" "${W}MControl Installer${NC}  ${C}v${MODULE_VERSION}${NC}  ${DIM}|${NC}  ${CH_COL}${CH_LBL}${NC}"
hdr_line "IPv4: ${IP4}  |  Port: ${PORT}" "${DIM}IPv4:${NC} ${M}${IP4}${NC}  ${DIM}|  Port:${NC} ${C}${PORT}${NC}"
hdr_line "IPv6: ${IP6}" "${DIM}IPv6:${NC} ${V6_COL}${IP6}${NC}"
printf "  ${FR}╚%s╝${NC}\n\n" "$BORDER"

echo -e "  ${FR}┌─[ INSTALLATION ]${NC}"
echo -e "  ${FR}│${NC}"

if ! command -v curl >/dev/null 2>&1; then
    echo -e "  ${FR}├─${NC} ${C}Installing curl & ca-certificates...${NC}"
    if command -v apt-get >/dev/null 2>&1; then
        apt-get update -y >/dev/null && apt-get install -y curl ca-certificates >/dev/null
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y curl ca-certificates >/dev/null
    fi
fi

mc_dl() {
    local url="$1" dest="$2"
    curl -fsSL --connect-timeout 8 --max-time 60 "$url" -o "$dest" 2>/dev/null \
        || curl -fsSL --connect-timeout 10 --max-time 60 "https://gh-proxy.com/${url}" -o "$dest" 2>/dev/null
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || pwd)"
BIN=""
CLI_SRC=""
VERSION_TXT=""

for cand in "$SCRIPT_DIR/mcontrol-linux-${ARCH}" "$SCRIPT_DIR/mcontrol-linux-amd64" "$SCRIPT_DIR/mcontrol"; do
    if [[ -f "$cand" ]] && head -c4 "$cand" 2>/dev/null | grep -q 'ELF'; then
        BIN="$cand"; break
    fi
done
[[ -f "$SCRIPT_DIR/mcontrol-cli.sh" ]] && CLI_SRC="$SCRIPT_DIR/mcontrol-cli.sh"
[[ -z "$CLI_SRC" && -f "$SCRIPT_DIR/main.sh" ]] && CLI_SRC="$SCRIPT_DIR/main.sh"
[[ -f "$SCRIPT_DIR/VERSION" ]] && VERSION_TXT="$(tr -d '[:space:]' < "$SCRIPT_DIR/VERSION")"

TMPD=""
cleanup() { [[ -n "$TMPD" ]] && rm -rf "$TMPD"; }
trap cleanup EXIT

if [[ -n "$BIN" ]]; then
    echo -e "  ${FR}├─${NC} ${C}Using local binary: ${W}${BIN}${NC}"
elif [[ -f "$SCRIPT_DIR/go.mod" ]]; then
    echo -e "  ${FR}├─${NC} ${C}Building single binary from local source...${NC}"
    cd "$SCRIPT_DIR"
    command -v go >/dev/null 2>&1 || export PATH="$PATH:/usr/local/go/bin"
    command -v go >/dev/null 2>&1 || { echo -e "  ${FR}└─${NC} ${R}✖ Go 1.22+ is required.${NC}"; exit 1; }
    go mod tidy
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o mcontrol .
    BIN="$SCRIPT_DIR/mcontrol"
else
    echo -e "  ${FR}├─${NC} ${C}Downloading pre-built binary (${ARCH} · ${CHANNEL})...${NC}"
    TMPD="$(mktemp -d)"
    BIN_NAME="mcontrol-linux-${ARCH}"
    if ! mc_dl "${RELEASE_BASE}/${BIN_NAME}" "${TMPD}/${BIN_NAME}"; then
        echo -e "  ${FR}└─${NC} ${R}✖ Download failed from GitHub & Mirror.${NC}"
        exit 1
    fi
    if mc_dl "${RELEASE_BASE}/${BIN_NAME}.sha256" "${TMPD}/${BIN_NAME}.sha256"; then
        (cd "$TMPD" && sha256sum -c "${BIN_NAME}.sha256" >/dev/null)
        echo -e "  ${FR}├─${NC} ${G}✓ SHA-256 verified.${NC}"
    fi
    BIN="${TMPD}/${BIN_NAME}"
    if mc_dl "${RELEASE_BASE}/mcontrol-cli.sh" "${TMPD}/mcontrol-cli.sh"; then
        CLI_SRC="${TMPD}/mcontrol-cli.sh"
    elif mc_dl "${RELEASE_BASE}/main.sh" "${TMPD}/main.sh"; then
        CLI_SRC="${TMPD}/main.sh"
    fi
    VERSION_TXT="$(curl -fsSL --connect-timeout 6 "${RELEASE_BASE}/VERSION" 2>/dev/null | tr -d '[:space:]' || echo "${MODULE_VERSION}")"
fi

[[ -n "$VERSION_TXT" ]] || VERSION_TXT="$MODULE_VERSION"

mkdir -p "$INSTALL_DIR" "$DATA_DIR"
chmod 700 "$DATA_DIR"
printf '%s\n' "$CHANNEL" > "${DATA_DIR}/.channel"
install -m 0755 "$BIN" "$INSTALL_DIR/mcontrol"
if [[ -n "$CLI_SRC" && -f "$CLI_SRC" ]]; then
    install -m 0755 "$CLI_SRC" /usr/local/bin/mcontrol
    echo -e "  ${FR}├─${NC} ${G}✓ Terminal menu installed (${W}mcontrol${G}).${NC}"
fi

ENV_LINE=""
[[ -n "${GOPROXY:-}" ]] && ENV_LINE="Environment=GOPROXY=${GOPROXY}"
cat > /etc/systemd/system/mcontrol.service <<UNIT
[Unit]
Description=MControl Central Panel (MDesign)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/mcontrol -port ${PORT} -data ${DATA_DIR}
Restart=always
RestartSec=2
LimitNOFILE=65535
${ENV_LINE}

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable mcontrol.service >/dev/null 2>&1
systemctl restart mcontrol.service

TOKEN=""
for _ in 1 2 3 4 5 6 7 8; do
    TOKEN=$(journalctl -u mcontrol.service -n 50 --no-pager 2>/dev/null | grep -o 'FIRST-TIME SETUP TOKEN: [0-9a-f]*' | tail -n1 | awk '{print $4}' || true)
    [[ -n "$TOKEN" ]] && break
    sleep 1
done

echo -e "  ${FR}├─${NC} ${G}✓ Systemd service active (v${VERSION_TXT}).${NC}"
echo -e "  ${FR}│${NC}"
echo -e "  ${FR}├─${NC} ${W}Setup URL   :${NC} ${C}http://${IP4}:${PORT}/${NC}"
if [[ -n "$TOKEN" ]]; then
    echo -e "  ${FR}├─${NC} ${W}Setup Token :${NC} ${Y}${TOKEN}${NC}"
else
    echo -e "  ${FR}├─${NC} ${W}Setup Token :${NC} ${DIM}already configured${NC}"
fi
echo -e "  ${FR}└─${NC} ${W}CLI Menu    :${NC} ${Y}mcontrol${NC}\n"
