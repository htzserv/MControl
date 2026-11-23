#!/usr/bin/env bash
# MControl v2 — MDesign Terminal Menu (Static IP-Only Header + Stable/Beta Channel Manager)
MODULE_VERSION="1.3.4"
set -u
umask 077

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    echo -e "\033[1;31m  ✖ Run MControl Menu with root privileges: sudo mcontrol-menu\033[0m" >&2
    exit 1
fi

# ── MDesign Palette (Matching MTunnel) ───────────────────────────────────────
B='\033[1;34m'
G='\033[1;32m'
Y='\033[1;33m'
R='\033[1;31m'
C='\033[0;36m'
M='\033[1;35m'
W='\033[1;37m'
DIM='\033[2;37m'
NC='\033[0m'
FR='\033[1;33m' # Gold frame & tree connectors

REPO="${MCONTROL_REPO:-htzserv/MControl}"
BIN="/usr/local/bin/mcontrol"
MENU_BIN="/usr/local/bin/mcontrol-menu"
DATA_DIR="/var/lib/mcontrol"
LOCAL_DIR="/root/mcontrol"
CHANNEL_FILE="${DATA_DIR}/.channel"
SERVICE="mcontrol"

# ── Helpers ──────────────────────────────────────────────────────────────────

mc_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *)             echo "" ;;
    esac
}

mc_get_channel() {
    if [[ -f "$CHANNEL_FILE" ]]; then
        local ch
        ch=$(tr -d '[:space:]' < "$CHANNEL_FILE" 2>/dev/null || true)
        if [[ "$ch" == "beta" ]]; then echo "beta"; return; fi
    fi
    echo "stable"
}

mc_channel_tag() {
    if [[ "$1" == "beta" ]]; then echo "v2-beta"; else echo "v2-latest"; fi
}

mc_get_port() {
    local p
    p=$(grep -oE -- '-port[[:space:]]+[0-9]+' "/etc/systemd/system/${SERVICE}.service" 2>/dev/null | awk '{print $2}' | head -n1 || true)
    [[ -n "$p" ]] && echo "$p" || echo "2053"
}

mc_detect_ips() {
    MC_IP4=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}' || true)
    if [[ -z "${MC_IP4:-}" ]]; then
        MC_IP4=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
    fi
    [[ -n "${MC_IP4:-}" ]] || MC_IP4="N/A"

    MC_IP6=$(ip -6 addr show scope global 2>/dev/null | awk '/inet6/{split($2,a,"/"); if(a[1] !~ /^f[cd]/){print a[1]; exit}}' || true)
    [[ -n "${MC_IP6:-}" ]] || MC_IP6="OFF"
}

mc_download() {
    local url="$1" dest="$2"
    if curl -fsSL --connect-timeout 8 --max-time 60 "$url" -o "$dest" 2>/dev/null; then
        return 0
    fi
    # Iran Mirror fallback via gh-proxy
    if curl -fsSL --connect-timeout 10 --max-time 60 "https://gh-proxy.com/${url}" -o "$dest" 2>/dev/null; then
        return 0
    fi
    return 1
}

# ── Static Header (MTunnel MDesign Gold Double-Line Frame — IPs Only, No Live Loop) ──

mc_hdr_line() {
    local plain="$1" colored="$2" w="$3"
    local LC_CTYPE=C.UTF-8
    if (( ${#plain} > w )); then
        plain="${plain:0:$((w-1))}…"
        colored="$plain"
    fi
    local pad=$(( w - ${#plain} ))
    (( pad < 0 )) && pad=0
    printf "  ${FR}║${NC} %b%*s ${FR}║${NC}\n" "$colored" "$pad" ""
}

draw_header() {
    clear 2>/dev/null || true
    mc_detect_ips

    local ch tag ch_label ch_col port
    ch=$(mc_get_channel)
    tag=$(mc_channel_tag "$ch")
    port=$(mc_get_port)
    if [[ "$ch" == "beta" ]]; then
        ch_label="BETA (${tag})"
        ch_col="$Y"
    else
        ch_label="STABLE (${tag})"
        ch_col="$G"
    fi

    local term_cols boxw content_w border
    term_cols=$(tput cols 2>/dev/null || echo 80)
    [[ "$term_cols" =~ ^[0-9]+$ ]] || term_cols=80
    boxw=$(( term_cols - 4 ))
    (( boxw < 46 )) && boxw=46
    (( boxw > 78 )) && boxw=78
    content_w=$(( boxw - 2 ))
    local inner_w=$(( content_w - 2 ))
    printf -v border '%*s' "$content_w" ''
    border="${border// /═}"

    local v6_col="$M"
    [[ "$MC_IP6" == "OFF" ]] && v6_col="$DIM"

    echo ""
    printf "  ${FR}╔%s╗${NC}\n" "$border"
    mc_hdr_line \
        "MControl v2  v${MODULE_VERSION}  |  ${ch_label}" \
        "${W}MControl v2${NC}  ${C}v${MODULE_VERSION}${NC}  ${DIM}|${NC}  ${ch_col}${ch_label}${NC}" \
        "$inner_w"
    mc_hdr_line \
        "IPv4: ${MC_IP4}  |  Port: ${port}" \
        "${DIM}IPv4:${NC} ${M}${MC_IP4}${NC}  ${DIM}|  Port:${NC} ${C}${port}${NC}" \
        "$inner_w"
    mc_hdr_line \
        "IPv6: ${MC_IP6}" \
        "${DIM}IPv6:${NC} ${v6_col}${MC_IP6}${NC}" \
        "$inner_w"
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
    printf '  %b├────────────────────────────%b\n' "$FR" "$NC"
    printf '  %b└─%b %b0%b %b❯%b %b%s%b\n\n' \
        "$FR" "$NC" "$W" "$NC" "$DIM" "$NC" "$DIM" "$1" "$NC"
}

mc_pause() {
    echo ""
    printf "  ${DIM}Press Enter to return to menu...${NC}"
    read -r _
}

# ── Actions ──────────────────────────────────────────────────────────────────

install_channel_binary() {
    local target_ch="$1"
    local tag arch base_url tmp_bin tmp_sha expected actual
    tag=$(mc_channel_tag "$target_ch")
    arch=$(mc_arch)
    if [[ -z "$arch" ]]; then
        echo -e "  ${R}✖ Unsupported architecture: $(uname -m)${NC}"
        return 1
    fi
    base_url="https://github.com/${REPO}/releases/download/${tag}"
    tmp_bin="${BIN}.new"
    tmp_sha="/tmp/mcontrol.sha256"

    echo -e "\n  ${C}● Downloading MControl (${target_ch^^} / ${tag}) for linux-${arch}...${NC}"
    if ! mc_download "${base_url}/mcontrol-linux-${arch}" "$tmp_bin"; then
        echo -e "  ${R}✖ Download failed from GitHub & Mirror.${NC}"
        rm -f "$tmp_bin"
        return 1
    fi

    if mc_download "${base_url}/mcontrol-linux-${arch}.sha256" "$tmp_sha"; then
        expected=$(awk '{print $1}' "$tmp_sha" | tr -d '[:space:]')
        actual=$(sha256sum "$tmp_bin" | awk '{print $1}')
        rm -f "$tmp_sha"
        if [[ -n "$expected" && "${expected,,}" != "${actual,,}" ]]; then
            echo -e "  ${R}✖ SHA-256 checksum mismatch! Aborting update.${NC}"
            rm -f "$tmp_bin"
            return 1
        fi
        echo -e "  ${G}✓ SHA-256 checksum verified.${NC}"
    fi

    chmod +x "$tmp_bin"
    mv -f "$tmp_bin" "$BIN"
    mkdir -p "$DATA_DIR" && chmod 700 "$DATA_DIR"
    printf '%s\n' "$target_ch" > "$CHANNEL_FILE"

    systemctl daemon-reload 2>/dev/null || true
    systemctl restart "$SERVICE" 2>/dev/null || systemctl enable --now "$SERVICE" 2>/dev/null || true
    echo -e "  ${G}✓ MControl updated to ${target_ch^^} (${tag}) and restarted.${NC}"
}

update_cli_scripts() {
    local ch tag base_url
    ch=$(mc_get_channel)
    tag=$(mc_channel_tag "$ch")
    base_url="https://github.com/${REPO}/releases/download/${tag}"
    mkdir -p "$LOCAL_DIR"
    echo -e "\n  ${C}● Updating terminal menu (main.sh) from ${tag}...${NC}"
    if mc_download "${base_url}/main.sh" "${LOCAL_DIR}/main.sh.tmp"; then
        chmod +x "${LOCAL_DIR}/main.sh.tmp"
        mv -f "${LOCAL_DIR}/main.sh.tmp" "${LOCAL_DIR}/main.sh"
        cp -f "${LOCAL_DIR}/main.sh" "$MENU_BIN"
        chmod +x "$MENU_BIN"
        echo -e "  ${G}✓ Terminal menu updated! Reloading...${NC}"
        sleep 1
        exec "$MENU_BIN"
    else
        rm -f "${LOCAL_DIR}/main.sh.tmp"
        echo -e "  ${R}✖ Could not download main.sh from ${tag}.${NC}"
    fi
}

change_panel_port() {
    local cur_port new_port
    cur_port=$(mc_get_port)
    echo ""
    printf "  ${C}Enter new panel port ${DIM}(current: ${cur_port})${C}: ${NC}"
    read -r new_port
    if ! [[ "$new_port" =~ ^[0-9]+$ ]] || (( new_port < 1 || new_port > 65535 )); then
        echo -e "  ${R}✖ Invalid port number.${NC}"
        return 1
    fi
    if [[ -f "/etc/systemd/system/${SERVICE}.service" ]]; then
        sed -i -E "s/-port[[:space:]]+[0-9]+/-port ${new_port}/" "/etc/systemd/system/${SERVICE}.service"
        systemctl daemon-reload
        systemctl restart "$SERVICE"
        echo -e "  ${G}✓ Panel port changed to ${new_port} and service restarted.${NC}"
    else
        echo -e "  ${R}✖ Service file not found. Run install.sh first.${NC}"
    fi
}

backup_data() {
    local ts out
    ts=$(date +%Y%m%d-%H%M%S)
    out="/root/mcontrol-backup-${ts}.tar.gz"
    if [[ ! -d "$DATA_DIR" ]]; then
        echo -e "  ${R}✖ Data directory ${DATA_DIR} does not exist.${NC}"
        return 1
    fi
    tar -czf "$out" -C "$(dirname "$DATA_DIR")" "$(basename "$DATA_DIR")"
    echo -e "  ${G}✓ Backup created: ${W}${out}${NC}"
}

uninstall_mcontrol() {
    echo ""
    printf "  ${R}⚠ Permanently remove MControl binary, service & databases? [yes/N]: ${NC}"
    local ans
    read -r ans
    if [[ "$ans" != "yes" ]]; then
        echo -e "  ${Y}● Uninstall cancelled.${NC}"
        return 0
    fi
    systemctl disable --now "$SERVICE" 2>/dev/null || true
    rm -f "/etc/systemd/system/${SERVICE}.service"
    systemctl daemon-reload 2>/dev/null || true
    rm -f "$BIN" "$MENU_BIN"
    rm -rf "$DATA_DIR" "$LOCAL_DIR"
    echo -e "\n  ${G}✓ MControl completely removed from this server.${NC}\n"
    exit 0
}

# ── Main Interactive Loop ────────────────────────────────────────────────────

while true; do
    draw_header
    cur_ch=$(mc_get_channel)

    mc_group "PANEL SERVICE" first
    mc_row "1" "Start / Restart Panel" "$G" " ${DIM}(systemd)${NC}"
    mc_row "2" "Stop Panel" "$Y"
    mc_row "3" "Service Status & Logs" "$C"
    mc_row "4" "Change Panel Port" "$M"

    mc_group "UPDATE & CHANNELS (STABLE / BETA)"
    mc_row "5" "Update on Current Channel" "$C" " ${DIM}(${cur_ch^^})${NC}"
    mc_row "6" "Switch to STABLE Channel" "$G" " ${DIM}(v2-latest)${NC}"
    mc_row "7" "Switch to BETA Channel" "$Y" " ${DIM}(Latest Action · v2-beta)${NC}"
    mc_row "8" "Update Terminal Menu" "$B" " ${DIM}(main.sh)${NC}"

    mc_group "MAINTENANCE"
    mc_row "9" "Backup Databases" "$C" " ${DIM}(/var/lib/mcontrol)${NC}"
    mc_row "10" "Uninstall MControl" "$R"

    mc_footer "Exit"

    printf "  ${C}Select ❯❯ ${NC}"
    read -r choice || exit 0
    case "$choice" in
        1)
            systemctl restart "$SERVICE" 2>/dev/null && echo -e "\n  ${G}✓ Panel restarted.${NC}" || echo -e "\n  ${R}✖ Failed to restart ${SERVICE}.${NC}"
            mc_pause ;;
        2)
            systemctl stop "$SERVICE" 2>/dev/null && echo -e "\n  ${Y}● Panel stopped.${NC}" || echo -e "\n  ${R}✖ Failed to stop ${SERVICE}.${NC}"
            mc_pause ;;
        3)
            echo ""
            systemctl status "$SERVICE" --no-pager -n 15 || true
            mc_pause ;;
        4)
            change_panel_port
            mc_pause ;;
        5)
            install_channel_binary "$cur_ch"
            mc_pause ;;
        6)
            install_channel_binary "stable"
            mc_pause ;;
        7)
            install_channel_binary "beta"
            mc_pause ;;
        8)
            update_cli_scripts
            mc_pause ;;
        9)
            backup_data
            mc_pause ;;
        10)
            uninstall_mcontrol
            mc_pause ;;
        0|q|Q)
            echo ""
            exit 0 ;;
        *)
            echo -e "  ${R}✖ Invalid selection.${NC}"
            sleep 1 ;;
    esac
done
