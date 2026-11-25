#!/usr/bin/env bash
# MControl v1.4.0 — MTunnel-Style MDesign Terminal Menu (Static IP-Only Header)
MODULE_VERSION="1.6.0"
set -u
umask 077

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    echo -e "\033[1;31m  ✖ Run MControl CLI with root privileges: sudo mcontrol\033[0m" >&2
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
FR='\033[1;33m' # Gold double-line frame & tree connectors

SVC="mcontrol.service"
REPO="${MCONTROL_REPO:-htzserv/MControl}"
INSTALL_DIR="/opt/mcontrol"
BIN="${INSTALL_DIR}/mcontrol"
CLI_BIN="/usr/local/bin/mcontrol"
DATA_DIR="/var/lib/mcontrol"
DB_FILE="${DATA_DIR}/mcontrol.db"
CHANNEL_FILE="${DATA_DIR}/.channel"

mc_arch() {
    case "$(uname -m)" in
        aarch64|arm64) echo "arm64" ;;
        *)             echo "amd64" ;;
    esac
}

db_get() {
    local key="$1"
    [[ -f "$DB_FILE" ]] || { echo ""; return; }
    python3 - "$DB_FILE" "$key" <<'PY' 2>/dev/null || true
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
row = con.execute("SELECT value FROM settings WHERE key=?", (sys.argv[2],)).fetchone()
print(row[0] if row else "")
PY
}

db_set() {
    local key="$1" val="$2"
    [[ -f "$DB_FILE" ]] || return 1
    python3 - "$DB_FILE" "$key" "$val" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
con.execute("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", (sys.argv[2], sys.argv[3]))
con.commit()
PY
}

mc_get_channel() {
    if [[ -f "$CHANNEL_FILE" ]]; then
        local ch
        ch=$(tr -d '[:space:]' < "$CHANNEL_FILE" 2>/dev/null || true)
        [[ "$ch" == "beta" ]] && { echo "beta"; return; }
    fi
    if [[ "$(db_get beta_builds)" == "1" ]]; then
        echo "beta"; return
    fi
    echo "stable"
}

mc_get_mirror() {
    local m
    m=$(db_get panel_mirror_url)
    [[ -n "$m" ]] || m="${MCONTROL_MIRROR_BASE:-https://c107328.parspack.net/c107328/MControl}"
    echo "${m%/}"
}

mc_channel_base() {
    local ch="$1" src="${2:-}"
    if [[ -z "$src" && -f "${DATA_DIR}/.source" ]]; then
        src=$(tr -d '[:space:]' < "${DATA_DIR}/.source" 2>/dev/null || true)
    fi
    if [[ "$src" == "mirror" ]]; then
        local mb; mb=$(mc_get_mirror)
        if [[ "$ch" == "beta" ]]; then echo "${mb}/beta-latest"; else echo "${mb}"; fi
    else
        if [[ "$ch" == "beta" ]]; then
            echo "https://github.com/${REPO}/releases/download/beta-latest"
        else
            echo "https://github.com/${REPO}/releases/latest/download"
        fi
    fi
}

mc_get_port() {
    local p
    p=$(db_get panel_port)
    [[ -n "$p" ]] || p=$(grep -oE -- '-port[[:space:]]+[0-9]+' /etc/systemd/system/mcontrol.service 2>/dev/null | awk '{print $2}' | head -n1 || true)
    [[ -n "$p" ]] && echo "$p" || echo "2053"
}

mc_detect_ips() {
    MC_IP4=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}' || true)
    [[ -n "${MC_IP4:-}" ]] || MC_IP4=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
    [[ -n "${MC_IP4:-}" ]] || MC_IP4="127.0.0.1"

    MC_IP6=$(ip -6 addr show scope global 2>/dev/null | awk '/inet6/{split($2,a,"/"); if(a[1] !~ /^f[cd]/){print a[1]; exit}}' || true)
    [[ -n "${MC_IP6:-}" ]] || MC_IP6="OFF"
}

mc_download() {
    local url="$1" dest="$2"
    curl -fsSL --connect-timeout 8 --max-time 60 "$url" -o "$dest" 2>/dev/null \
        || curl -fsSL --connect-timeout 10 --max-time 60 "https://gh-proxy.com/${url}" -o "$dest" 2>/dev/null
}

# ── Static Gold Double-Line Header (IPs Only, No Live Refresh Loop) ──────────

mc_hdr_line() {
    local plain="$1" colored="$2" w="$3" LC_CTYPE=C.UTF-8
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

    local ch ch_lbl ch_col port
    ch=$(mc_get_channel)
    port=$(mc_get_port)
    if [[ "$ch" == "beta" ]]; then
        ch_lbl="BETA (beta-latest)"
        ch_col="$Y"
    else
        ch_lbl="STABLE (latest)"
        ch_col="$G"
    fi

    local term_cols boxw content_w inner_w border
    term_cols=$(tput cols 2>/dev/null || echo 80)
    [[ "$term_cols" =~ ^[0-9]+$ ]] || term_cols=80
    boxw=$(( term_cols - 4 ))
    (( boxw < 46 )) && boxw=46
    (( boxw > 78 )) && boxw=78
    content_w=$(( boxw - 2 ))
    inner_w=$(( content_w - 2 ))
    printf -v border '%*s' "$content_w" ''
    border="${border// /═}"

    local v6_col="$M"
    [[ "$MC_IP6" == "OFF" ]] && v6_col="$DIM"

    echo ""
    printf "  ${FR}╔%s╗${NC}\n" "$border"
    mc_hdr_line \
        "MControl  v${MODULE_VERSION}  |  ${ch_lbl}" \
        "${W}MControl${NC}  ${C}v${MODULE_VERSION}${NC}  ${DIM}|${NC}  ${ch_col}${ch_lbl}${NC}" \
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

install_channel() {
    local target_ch="$1"
    local arch base_url tmp_bin tmp_sha exp got
    arch=$(mc_arch)
    base_url=$(mc_channel_base "$target_ch")
    tmp_bin="${BIN}.new"
    tmp_sha="/tmp/mcontrol.sha256"

    mkdir -p "$INSTALL_DIR" "$DATA_DIR"
    echo -e "\n  ${C}● Downloading MControl (${target_ch^^}) for linux-${arch}...${NC}"
    if ! mc_download "${base_url}/mcontrol-linux-${arch}" "$tmp_bin"; then
        echo -e "  ${R}✖ Download failed from GitHub & Mirror.${NC}"
        rm -f "$tmp_bin"
        return 1
    fi
    if mc_download "${base_url}/mcontrol-linux-${arch}.sha256" "$tmp_sha"; then
        exp=$(awk '{print $1}' "$tmp_sha" | tr -d '[:space:]')
        got=$(sha256sum "$tmp_bin" | awk '{print $1}')
        rm -f "$tmp_sha"
        if [[ -n "$exp" && "${exp,,}" != "${got,,}" ]]; then
            echo -e "  ${R}✖ SHA-256 mismatch! Update aborted.${NC}"
            rm -f "$tmp_bin"
            return 1
        fi
        echo -e "  ${G}✓ SHA-256 checksum verified.${NC}"
    fi
    chmod 755 "$tmp_bin"
    mv -f "$tmp_bin" "$BIN"
    printf '%s\n' "$target_ch" > "$CHANNEL_FILE"
    if [[ "$target_ch" == "beta" ]]; then db_set beta_builds "1" || true; else db_set beta_builds "0" || true; fi

    if mc_download "${base_url}/mcontrol-cli.sh" "${CLI_BIN}.new"; then
        chmod 755 "${CLI_BIN}.new"
        mv -f "${CLI_BIN}.new" "$CLI_BIN"
    else
        rm -f "${CLI_BIN}.new"
    fi

    systemctl daemon-reload 2>/dev/null || true
    systemctl restart "$SVC" 2>/dev/null || systemctl enable --now "$SVC" 2>/dev/null || true
    echo -e "  ${G}✓ Updated to ${target_ch^^} and service restarted.${NC}"
}

show_settings() {
    local ch port wpath dom strict_dom tls_state twofa_state probe_iv repo_url mirror_url svc_name active_url
    mc_detect_ips
    ch=$(mc_get_channel)
    port=$(mc_get_port)
    wpath=$(db_get web_path)
    dom=$(db_get listen_domain)
    strict_dom=$(db_get strict_domain)
    twofa_state=$(db_get twofa_enabled)
    probe_iv=$(db_get probe_interval)
    repo_url=$(db_get github_repo_url)
    mirror_url=$(db_get iran_mirror_url)
    svc_name=$(db_get service_name)

    if [[ -f "$DATA_DIR/tls.crt" && -f "$DATA_DIR/tls.key" ]]; then
        tls_state="ENABLED (HTTPS)"
    else
        tls_state="DISABLED (HTTP)"
    fi

    local host_for_url="$MC_IP4"
    [[ -n "$dom" ]] && host_for_url="$dom"
    local scheme="http"
    [[ "$tls_state" == *"HTTPS"* ]] && scheme="https"
    active_url="${scheme}://${host_for_url}:${port}/${wpath:+${wpath}/}"

    echo ""
    printf "  ${FR}┌─[ PANEL SETTINGS — CREDENTIALS HIDDEN ]${NC}\n"
    printf "  ${FR}│${NC}\n"
    printf "  ${FR}├─${NC} ${DIM}Channel        :${NC} ${Y}%s${NC}\n" "$ch"
    printf "  ${FR}├─${NC} ${DIM}Panel URL      :${NC} ${C}%s${NC}\n" "$active_url"
    printf "  ${FR}├─${NC} ${DIM}Active Port    :${NC} ${W}%s${NC}\n" "$port"
    printf "  ${FR}├─${NC} ${DIM}Secret WebPath :${NC} ${W}/%s/${NC}\n" "${wpath:-<auto>}"
    printf "  ${FR}├─${NC} ${DIM}Listen Domain  :${NC} ${W}%s${NC}\n" "${dom:-<not set>}"
    printf "  ${FR}├─${NC} ${DIM}Strict Domain  :${NC} ${W}%s${NC}\n" "${strict_dom:-0}"
    printf "  ${FR}├─${NC} ${DIM}HTTPS / TLS    :${NC} ${G}%s${NC}\n" "$tls_state"
    printf "  ${FR}├─${NC} ${DIM}2FA (TOTP)     :${NC} ${W}%s${NC}\n" "${twofa_state:-0}"
    printf "  ${FR}├─${NC} ${DIM}Probe Interval :${NC} ${W}%s min${NC}\n" "${probe_iv:-0}"
    printf "  ${FR}├─${NC} ${DIM}Agent Service  :${NC} ${W}%s${NC}\n" "${svc_name:-mcontrol-agent}"
    printf "  ${FR}├─${NC} ${DIM}GitHub Repo URL:${NC} ${DIM}%s${NC}\n" "${repo_url:-<default>}"
    printf "  ${FR}├─${NC} ${DIM}Iran Mirror URL:${NC} ${DIM}%s${NC}\n" "${mirror_url:-<default>}"
    printf "  ${FR}├─${NC} ${DIM}IPv4 Address   :${NC} ${M}%s${NC}\n" "$MC_IP4"
    printf "  ${FR}├─${NC} ${DIM}IPv6 Address   :${NC} ${M}%s${NC}\n" "$MC_IP6"
    printf "  ${FR}├─${NC} ${DIM}Data Directory :${NC} ${W}%s${NC}\n" "$DATA_DIR"
    printf "  ${FR}└─${NC} ${DIM}Credentials    :${NC} ${Y}[HIDDEN — User & Password Excluded]${NC}\n"
}

while true; do
    draw_header
    cur_ch=$(mc_get_channel)

    mc_group "PANEL SERVICE" first
    mc_row "1"  "Restart MControl Service" "$G"
    mc_row "2"  "Start MControl Service" "$C"
    mc_row "3"  "Stop MControl Service" "$Y"
    mc_row "4"  "View Service Logs (last 40 lines)" "$C"
    mc_row "44" "Show Settings (Excl. User/Pass)" "$C" " ${DIM}(No Secrets)${NC}"

    mc_group "ACCESS & SECURITY"
    mc_row "5"  "Change Panel Port" "$M" " ${DIM}(SQLite + systemd)${NC}"
    mc_row "6"  "Change Secret Web Path" "$M"
    mc_row "7"  "Disable 2FA" "$Y" " ${DIM}(Emergency Reset)${NC}"
    mc_row "8"  "Disable HTTPS" "$Y" " ${DIM}(Switch back to HTTP)${NC}"

    mc_group "UPDATE & CHANNELS (STABLE / BETA)"
    mc_row "9"  "Update on Current Channel" "$C" " ${DIM}(${cur_ch^^})${NC}"
    mc_row "10" "Switch to STABLE Channel" "$G" " ${DIM}(releases/latest)${NC}"
    mc_row "11" "Switch to BETA Channel" "$Y" " ${DIM}(Rolling Action · beta-latest)${NC}"

    mc_group "MAINTENANCE"
    mc_row "12" "Backup All Databases" "$B" " ${DIM}(/var/lib/mcontrol)${NC}"
    mc_row "13" "Uninstall MControl" "$R"

    mc_footer "Exit"

    printf "  ${C}Select ❯❯ ${NC}"
    read -r opt || exit 0
    case "$opt" in
        1) systemctl restart "$SVC" && echo -e "\n  ${G}✓ Service restarted.${NC}" || echo -e "\n  ${R}✖ Restart failed.${NC}"; mc_pause ;;
        2) systemctl start "$SVC" && echo -e "\n  ${G}✓ Service started.${NC}" || echo -e "\n  ${R}✖ Start failed.${NC}"; mc_pause ;;
        3) systemctl stop "$SVC" && echo -e "\n  ${Y}● Service stopped.${NC}" || echo -e "\n  ${R}✖ Stop failed.${NC}"; mc_pause ;;
        4) echo ""; journalctl -u "$SVC" -n 40 --no-pager || true; mc_pause ;;
        44) show_settings; mc_pause ;;
        5)
            echo ""
            printf "  ${C}Enter new port (1-65535): ${NC}"
            read -r newp
            if [[ "$newp" =~ ^[0-9]+$ ]] && (( newp >= 1 && newp <= 65535 )); then
                db_set panel_port "$newp" || true
                sed -i -E "s/-port [0-9]+/-port ${newp}/" /etc/systemd/system/mcontrol.service 2>/dev/null || true
                systemctl daemon-reload
                systemctl restart "$SVC"
                echo -e "  ${G}✓ Panel port updated to ${newp}.${NC}"
            else
                echo -e "  ${R}✖ Invalid port.${NC}"
            fi
            mc_pause ;;
        6)
            echo ""
            printf "  ${C}Enter new web path (3-48 chars, e.g. mc_panel1): ${NC}"
            read -r newwp
            newwp="${newwp#/}"; newwp="${newwp%/}"
            if [[ "$newwp" =~ ^[A-Za-z0-9_-]{3,48}$ ]]; then
                db_set web_path "$newwp" && systemctl restart "$SVC"
                echo -e "  ${G}✓ Web path updated to /${newwp}/ and service restarted.${NC}"
            else
                echo -e "  ${R}✖ Invalid web path format.${NC}"
            fi
            mc_pause ;;
        7)
            db_set twofa_enabled "0" && echo -e "\n  ${G}✓ 2FA disabled.${NC}" || echo -e "\n  ${R}✖ Database not found.${NC}"
            mc_pause ;;
        8)
            rm -f "$DATA_DIR/tls.crt" "$DATA_DIR/tls.key" && systemctl restart "$SVC"
            echo -e "\n  ${Y}● HTTPS certificates removed. Switched to HTTP.${NC}"
            mc_pause ;;
        9)  install_channel "$cur_ch"; mc_pause ;;
        10) install_channel "stable"; mc_pause ;;
        11) install_channel "beta"; mc_pause ;;
        12)
            out="/root/mcontrol-backup-$(date +%Y%m%d-%H%M%S).tar.gz"
            if [[ -d "$DATA_DIR" ]]; then
                tar -czf "$out" -C "$(dirname "$DATA_DIR")" "$(basename "$DATA_DIR")"
                echo -e "\n  ${G}✓ Backup created: ${W}${out}${NC}"
            else
                echo -e "\n  ${R}✖ ${DATA_DIR} not found.${NC}"
            fi
            mc_pause ;;
        13)
            echo ""
            printf "  ${R}⚠ Uninstall MControl? [y/N]: ${NC}"
            read -r yn
            if [[ "$yn" =~ ^[Yy]$ ]]; then
                systemctl disable --now "$SVC" 2>/dev/null || true
                rm -f /etc/systemd/system/mcontrol.service "$CLI_BIN"
                rm -rf "$INSTALL_DIR"
                systemctl daemon-reload 2>/dev/null || true
                echo -e "\n  ${Y}✓ MControl uninstalled (databases kept at ${DATA_DIR}).${NC}\n"
                exit 0
            fi
            mc_pause ;;
        0|q|Q) echo ""; exit 0 ;;
        *) echo -e "  ${R}✖ Invalid selection.${NC}"; sleep 1 ;;
    esac
done
