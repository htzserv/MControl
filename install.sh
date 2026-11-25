#!/usr/bin/env bash
# MControl v1.5.2 — 2-Step MDesign Interactive Installer (Step 1: Iran/Foreign -> Step 2: Stable/Beta ± Engines)
MODULE_VERSION="1.5.2"
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
    echo -e "  ${R}✖ Run as root: sudo bash install.sh [--wipe] [--mirror|--direct] [--stable|--beta|--full|--full-beta|--bundle] [port]${NC}" >&2
    exit 1
fi

INSTALL_DIR="/opt/mcontrol"
DATA_DIR="/var/lib/mcontrol"
CACHE_BUNDLE_DIR="${DATA_DIR}/cache/bundle"
BUNDLE_FILE="mcontrol-bundle-linux-amd64.tar.gz"

REPO="${MCONTROL_REPO:-htzserv/MControl}"
MIRROR_BASE="${MCONTROL_MIRROR_BASE:-https://c107328.parspack.net/c107328/MControl}"
MIRROR_BASE="${MIRROR_BASE%/}"

GH_STABLE_BASE="https://github.com/${REPO}/releases/latest/download"
GH_BETA_BASE="https://github.com/${REPO}/releases/download/beta-latest"
GH_BUNDLE_BASE="https://github.com/${REPO}/releases/download/bundle-latest"

MIRROR_STABLE_BASE="${MIRROR_BASE}"
MIRROR_BETA_BASE="${MIRROR_BASE}/beta-latest"
MIRROR_BUNDLE_BASE="${MIRROR_BASE}"

PORT=""
DO_WIPE=0
SOURCE=""        # mirror | direct
CHANNEL=""       # stable | beta
WITH_ENGINES=0   # 0 | 1
BUNDLE_ONLY=0    # 0 | 1
INTERACTIVE=1

for arg in "$@"; do
    case "$arg" in
        --wipe|wipe)             DO_WIPE=1 ;;
        --mirror|iran)           SOURCE="mirror"; INTERACTIVE=0 ;;
        --direct|foreign|github) SOURCE="direct"; INTERACTIVE=0 ;;
        --stable|stable)         CHANNEL="stable"; WITH_ENGINES=0; INTERACTIVE=0 ;;
        --full|full-stable)      CHANNEL="stable"; WITH_ENGINES=1; INTERACTIVE=0 ;;
        --beta|beta)             CHANNEL="beta";   WITH_ENGINES=0; INTERACTIVE=0 ;;
        --full-beta|beta-full)   CHANNEL="beta";   WITH_ENGINES=1; INTERACTIVE=0 ;;
        --bundle|engines)        BUNDLE_ONLY=1;    WITH_ENGINES=1; INTERACTIVE=0 ;;
        [0-9]*)                  PORT="$arg" ;;
    esac
done

if [[ -z "$PORT" ]]; then
    PORT=$((20000 + RANDOM % 35000))
fi

case "$(uname -m)" in
    aarch64|arm64) ARCH="arm64" ;;
    *)             ARCH="amd64" ;;
esac

# ── Static IP Detection ──────────────────────────────────────────────────────
IP4=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}' || true)
[[ -n "${IP4:-}" ]] || IP4=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
[[ -n "${IP4:-}" ]] || IP4="127.0.0.1"
IP6=$(ip -6 addr show scope global 2>/dev/null | awk '/inet6/{split($2,a,"/"); if(a[1] !~ /^f[cd]/){print a[1]; exit}}' || true)
[[ -n "${IP6:-}" ]] || IP6="OFF"

draw_banner() {
    local sub_lbl="${1:-INSTALL MENU}" sub_col="${2:-$C}"
    local v6_col="$M"; [[ "$IP6" == "OFF" ]] && v6_col="$DIM"
    local boxw=68 border
    printf -v border '%*s' "$((boxw - 2))" ''
    border="${border// /═}"
    hdr_line() {
        local plain="$1" colored="$2" w=$((boxw - 4)) LC_CTYPE=C.UTF-8
        (( ${#plain} > w )) && { plain="${plain:0:$((w-1))}…"; colored="$plain"; }
        printf "  ${FR}║${NC} %b%*s ${FR}║${NC}\n" "$colored" "$((w - ${#plain}))" ""
    }
    clear 2>/dev/null || true
    echo ""
    printf "  ${FR}╔%s╗${NC}\n" "$border"
    hdr_line "MControl Installer  v${MODULE_VERSION}  |  ${sub_lbl}" "${W}MControl Installer${NC}  ${C}v${MODULE_VERSION}${NC}  ${DIM}|${NC}  ${sub_col}${sub_lbl}${NC}"
    hdr_line "IPv4: ${IP4}  |  Port: ${PORT}" "${DIM}IPv4:${NC} ${M}${IP4}${NC}  ${DIM}|  Port:${NC} ${C}${PORT}${NC}"
    hdr_line "IPv6: ${IP6}" "${DIM}IPv6:${NC} ${v6_col}${IP6}${NC}"
    printf "  ${FR}╚%s╝${NC}\n" "$border"
}

mc_group() {
    if [[ "${2:-}" == "first" ]]; then
        printf '\n  %b┌─[ %s ]%b\n' "$FR" "$1" "$NC"
    else
        printf '  %b│%b\n' "$FR" "$NC"
        printf '  %b├─[ %s ]%b\n' "$FR" "$1" "$NC"
    fi
    printf '  %b│%b\n' "$FR" "$NC"
}

mc_row() {
    local id="$1" label="$2" col="${3:-$C}" note="${4:-}"
    printf '  %b├─%b %b%-2s%b%b❯%b %b%s%b%b\n' \
        "$FR" "$NC" "$W" "$id" "$NC" "$DIM" "$NC" "$col" "$label" "$NC" "$note"
}

mc_footer() {
    printf '  %b│%b\n' "$FR" "$NC"
    printf '  %b├────────────────────────────────────────%b\n' "$FR" "$NC"
    printf '  %b└─%b %b0%b %b❯%b %b%s%b\n\n' \
        "$FR" "$NC" "$W" "$NC" "$DIM" "$NC" "$DIM" "$1" "$NC"
}

# ── 2-Step Interactive Wizard (Step 1: Source -> Step 2: Channel & Engines) ──
if [[ "$INTERACTIVE" -eq 1 ]] && [[ -r /dev/tty ]]; then
    STEP=1
    while true; do
        if [[ "$STEP" -eq 1 ]]; then
            draw_banner "STEP 1 / 2 · DOWNLOAD SOURCE" "$Y"

            mc_group "SELECT DOWNLOAD SOURCE (IRAN / FOREIGN)" first
            mc_row "1" "Iran Mirror (ParsPack)"  "$G" " ${DIM}(Direct & Fast inside Iran)${NC}"
            mc_row "2" "Foreign (GitHub Direct)" "$C" " ${DIM}(Official GitHub Releases)${NC}"

            mc_footer "Exit"

            printf "  ${C}Select source [1-2, 0] ❯❯ ${NC}"
            read -r s_choice < /dev/tty || exit 0
            case "$s_choice" in
                1) SOURCE="mirror"; STEP=2 ;;
                2) SOURCE="direct"; STEP=2 ;;
                0|q|Q) echo ""; exit 0 ;;
                *) echo -e "  ${R}✖ Invalid selection.${NC}"; sleep 1 ;;
            esac
        else
            local_src_lbl="FOREIGN (GITHUB)"; local_src_col="$C"
            [[ "$SOURCE" == "mirror" ]] && { local_src_lbl="IRAN MIRROR"; local_src_col="$G"; }

            draw_banner "STEP 2 / 2 · ${local_src_lbl}" "$local_src_col"

            mc_group "SELECT INSTALL PACKAGE (${local_src_lbl})" first
            mc_row "1" "Stable"                  "$G" " ${DIM}(Official Release · Panel Only)${NC}"
            mc_row "2" "Stable + Engines Bundle" "$G" " ${DIM}(Official Release + Static Engines)${NC}"
            mc_row "3" "Beta"                    "$Y" " ${DIM}(Rolling beta-latest · Panel Only)${NC}"
            mc_row "4" "Beta + Engines Bundle"   "$Y" " ${DIM}(Rolling beta-latest + Static Engines)${NC}"

            mc_group "NAVIGATION"
            mc_row "9" "Back to Source Selection" "$DIM" ""

            mc_footer "Exit"

            printf "  ${C}Select package [1-4, 9=Back, 0=Exit] ❯❯ ${NC}"
            read -r p_choice < /dev/tty || exit 0
            case "$p_choice" in
                1) CHANNEL="stable"; WITH_ENGINES=0; break ;;
                2) CHANNEL="stable"; WITH_ENGINES=1; break ;;
                3) CHANNEL="beta";   WITH_ENGINES=0; break ;;
                4) CHANNEL="beta";   WITH_ENGINES=1; break ;;
                9|b|B) STEP=1 ;;
                0|q|Q) echo ""; exit 0 ;;
                *) echo -e "  ${R}✖ Invalid selection.${NC}"; sleep 1 ;;
            esac
        fi
    done
fi

[[ -n "$CHANNEL" ]] || CHANNEL="stable"
[[ -n "$SOURCE" ]]  || SOURCE="direct"

# ── Resolve Download Bases ───────────────────────────────────────────────────
if [[ "$SOURCE" == "mirror" ]]; then
    SRC_LBL="IRAN MIRROR"
    BUNDLE_BASE="$MIRROR_BUNDLE_BASE"
    if [[ "$CHANNEL" == "beta" ]]; then
        RELEASE_BASE="$MIRROR_BETA_BASE"
    else
        RELEASE_BASE="$MIRROR_STABLE_BASE"
    fi
else
    SRC_LBL="FOREIGN (GITHUB)"
    BUNDLE_BASE="$GH_BUNDLE_BASE"
    if [[ "$CHANNEL" == "beta" ]]; then
        RELEASE_BASE="$GH_BETA_BASE"
    else
        RELEASE_BASE="$GH_STABLE_BASE"
    fi
fi

[[ -n "${MCONTROL_RELEASE_BASE:-}" ]] && RELEASE_BASE="$MCONTROL_RELEASE_BASE"

PKG_LBL="${CHANNEL^^}"
[[ "$WITH_ENGINES" -eq 1 ]] && PKG_LBL="${CHANNEL^^} + ENGINES"
[[ "$BUNDLE_ONLY" -eq 1 ]] && PKG_LBL="ENGINES BUNDLE"

draw_banner "${PKG_LBL} · ${SRC_LBL}" "$( [[ "$SOURCE" == "mirror" ]] && echo "$G" || echo "$C" )"
echo ""
echo -e "  ${FR}┌─[ EXECUTION ]${NC}"
echo -e "  ${FR}│${NC}"

if [[ "$DO_WIPE" -eq 1 && "$BUNDLE_ONLY" -eq 0 ]]; then
    echo -e "  ${FR}├─${NC} ${Y}Wiping previous MControl binaries, services & databases...${NC}"
    systemctl disable --now mcontrol.service 2>/dev/null || true
    rm -f /etc/systemd/system/mcontrol.service /usr/local/bin/mcontrol /usr/local/bin/mcontrol-menu
    rm -rf /opt/mcontrol /var/lib/mcontrol /root/mcontrol /etc/mcontrol
    systemctl daemon-reload 2>/dev/null || true
    echo -e "  ${FR}├─${NC} ${G}✓ Previous installation completely wiped.${NC}"
fi

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
    if [[ "$SOURCE" == "mirror" ]]; then
        curl -fsSL --connect-timeout 10 --max-time 180 "$url" -o "$dest" 2>/dev/null
    else
        curl -fsSL --connect-timeout 10 --max-time 180 "$url" -o "$dest" 2>/dev/null \
            || curl -fsSL --connect-timeout 10 --max-time 180 "https://gh-proxy.com/${url}" -o "$dest" 2>/dev/null
    fi
}

install_engines_bundle() {
    mkdir -p "$CACHE_BUNDLE_DIR" /usr/local/bin
    local dst="${CACHE_BUNDLE_DIR}/${BUNDLE_FILE}"
    local tmp_b="${dst}.tmp"
    local tmp_sha="${dst}.sha256"

    echo -e "  ${FR}├─${NC} ${C}Downloading Static Engines Bundle (${SRC_LBL})...${NC}"
    if ! mc_dl "${BUNDLE_BASE}/${BUNDLE_FILE}" "$tmp_b"; then
        rm -f "$tmp_b"
        echo -e "  ${FR}├─${NC} ${R}✖ Bundle download failed from ${BUNDLE_BASE}/${BUNDLE_FILE}${NC}"
        return 1
    fi
    if mc_dl "${BUNDLE_BASE}/${BUNDLE_FILE}.sha256" "$tmp_sha"; then
        local exp got
        exp=$(awk '{print $1}' "$tmp_sha" | tr -d '[:space:]')
        got=$(sha256sum "$tmp_b" | awk '{print $1}')
        rm -f "$tmp_sha"
        if [[ -n "$exp" && "${exp,,}" != "${got,,}" ]]; then
            rm -f "$tmp_b"
            echo -e "  ${FR}├─${NC} ${R}✖ Bundle SHA-256 mismatch! Aborted.${NC}"
            return 1
        fi
        echo -e "  ${FR}├─${NC} ${G}✓ Bundle SHA-256 verified.${NC}"
    fi
    mv -f "$tmp_b" "$dst"
    chmod 644 "$dst"
    echo -e "  ${FR}├─${NC} ${G}✓ Cached in Master Cache: ${W}${dst}${NC}"

    if tar -tzf "$dst" >/dev/null 2>&1; then
        local ext_tmp
        ext_tmp="$(mktemp -d)"
        tar -xzf "$dst" -C "$ext_tmp" 2>/dev/null || true
        if [[ -d "${ext_tmp}/bin" ]]; then
            for f in "${ext_tmp}"/bin/*; do
                [[ -f "$f" ]] && install -m 0755 "$f" "/usr/local/bin/$(basename "$f")"
            done
            echo -e "  ${FR}├─${NC} ${G}✓ Static engines extracted to /usr/local/bin.${NC}"
        fi
        rm -rf "$ext_tmp"
    fi
}

if [[ "$BUNDLE_ONLY" -eq 1 ]]; then
    install_engines_bundle
    echo -e "  ${FR}└─${NC} ${G}✓ Static Engines & Prereqs Bundle ready.${NC}\n"
    exit 0
fi

# ── Panel Installation (Stable / Beta, with optional Static Engines Bundle) ──
TMPD="$(mktemp -d)"
cleanup() { [[ -n "${TMPD:-}" ]] && rm -rf "$TMPD"; }
trap cleanup EXIT

BIN_NAME="mcontrol-linux-${ARCH}"
echo -e "  ${FR}├─${NC} ${C}Downloading MControl (${CHANNEL^^} · ${ARCH}) from ${SRC_LBL}...${NC}"
if ! mc_dl "${RELEASE_BASE}/${BIN_NAME}" "${TMPD}/${BIN_NAME}"; then
    echo -e "  ${FR}└─${NC} ${R}✖ Download failed from ${RELEASE_BASE}/${BIN_NAME}${NC}"
    exit 1
fi
if mc_dl "${RELEASE_BASE}/${BIN_NAME}.sha256" "${TMPD}/${BIN_NAME}.sha256"; then
    (cd "$TMPD" && sha256sum -c "${BIN_NAME}.sha256" >/dev/null)
    echo -e "  ${FR}├─${NC} ${G}✓ Panel binary SHA-256 verified.${NC}"
fi
BIN="${TMPD}/${BIN_NAME}"

CLI_SRC=""
if mc_dl "${RELEASE_BASE}/mcontrol-cli.sh" "${TMPD}/mcontrol-cli.sh"; then
    CLI_SRC="${TMPD}/mcontrol-cli.sh"
elif mc_dl "${RELEASE_BASE}/main.sh" "${TMPD}/main.sh"; then
    CLI_SRC="${TMPD}/main.sh"
fi
VERSION_TXT="$(mc_dl "${RELEASE_BASE}/VERSION" "${TMPD}/VERSION" && tr -d '[:space:]' < "${TMPD}/VERSION" || echo "${MODULE_VERSION}")"
[[ -n "$VERSION_TXT" ]] || VERSION_TXT="$MODULE_VERSION"

mkdir -p "$INSTALL_DIR" "$DATA_DIR"
chmod 700 "$DATA_DIR"
printf '%s\n' "$CHANNEL" > "${DATA_DIR}/.channel"
printf '%s\n' "$SOURCE"  > "${DATA_DIR}/.source"
install -m 0755 "$BIN" "$INSTALL_DIR/mcontrol"
if [[ -n "$CLI_SRC" && -f "$CLI_SRC" ]]; then
    install -m 0755 "$CLI_SRC" /usr/local/bin/mcontrol
    echo -e "  ${FR}├─${NC} ${G}✓ Terminal menu installed (${W}mcontrol${G}).${NC}"
fi

if [[ "$WITH_ENGINES" -eq 1 ]]; then
    install_engines_bundle || echo -e "  ${FR}├─${NC} ${Y}⚠ Bundle step skipped (can retry later).${NC}"
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
ExecStopPost=/bin/bash -c 'if [ "\$\$SERVICE_RESULT" != "success" ] && [ -f ${DATA_DIR}/.update_pending ] && [ -f ${INSTALL_DIR}/mcontrol.old ]; then mv -f ${INSTALL_DIR}/mcontrol.old ${INSTALL_DIR}/mcontrol && chmod 0755 ${INSTALL_DIR}/mcontrol && rm -f ${DATA_DIR}/.update_pending; fi'
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

echo -e "  ${FR}├─${NC} ${G}✓ Systemd service active (v${VERSION_TXT} · ${PKG_LBL} · ${SRC_LBL}).${NC}"
echo -e "  ${FR}│${NC}"
echo -e "  ${FR}├─${NC} ${W}Setup URL   :${NC} ${C}http://${IP4}:${PORT}/${NC}"
if [[ -n "$TOKEN" ]]; then
    echo -e "  ${FR}├─${NC} ${W}Setup Token :${NC} ${Y}${TOKEN}${NC}"
else
    echo -e "  ${FR}├─${NC} ${W}Setup Token :${NC} ${DIM}already configured${NC}"
fi
echo -e "  ${FR}└─${NC} ${W}CLI Menu    :${NC} ${Y}mcontrol${NC}\n"
