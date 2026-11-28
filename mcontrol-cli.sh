#!/usr/bin/env bash
# MControl v1.4.0 — MTunnel-Style MDesign Terminal Menu (Static IP-Only Header)
MODULE_VERSION="1.8.1.2"
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

reset_panel_settings() {
    if [[ ! -f "$DB_FILE" ]]; then
        echo -e "\n  ${R}✖ Database not found at ${DB_FILE}.${NC}"
        return 1
    fi
    echo ""
    echo -e "  ${Y}⚠ This resets panel domain, strict domain, secret web path, 2FA, and removes TLS certs.${NC}"
    printf "  ${C}Reset panel domain & web settings now? [y/N]: ${NC}"
    read -r yn
    [[ "$yn" =~ ^[Yy]$ ]] || { echo -e "  ${DIM}Cancelled.${NC}"; return 0; }

    if command -v python3 >/dev/null 2>&1; then
        python3 - "$DB_FILE" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
for k, v in [
    ("listen_domain", ""),
    ("strict_domain", "0"),
    ("web_path", ""),
    ("twofa_enabled", "0"),
]:
    con.execute("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", (k, v))
con.commit()
con.close()
PY
    fi
    rm -f "$DATA_DIR/tls.crt" "$DATA_DIR/tls.key"
    systemctl restart "$SVC" 2>/dev/null || true
    echo -e "  ${G}✓ Panel settings (domain, web path, 2FA, HTTPS) reset and service restarted.${NC}"
}

reset_panel_credentials() {
    if [[ ! -f "$DB_FILE" ]]; then
        echo -e "\n  ${R}✖ Database not found at ${DB_FILE}.${NC}"
        return 1
    fi
    if ! command -v python3 >/dev/null 2>&1; then
        echo -e "\n  ${R}✖ python3 is required to reset credentials.${NC}"
        return 1
    fi
    echo ""
    printf "  ${C}New panel username (leave empty to keep current): ${NC}"
    read -r new_user
    new_user=$(printf '%s' "$new_user" | xargs)
    printf "  ${C}New panel password (min 8 chars): ${NC}"
    read -rs new_pass
    echo ""
    if [[ ${#new_pass} -lt 8 ]]; then
        echo -e "  ${R}✖ Password must be at least 8 characters.${NC}"
        return 1
    fi
    printf "  ${C}Confirm new password: ${NC}"
    read -rs new_pass2
    echo ""
    if [[ "$new_pass" != "$new_pass2" ]]; then
        echo -e "  ${R}✖ Passwords do not match.${NC}"
        return 1
    fi
    python3 - "$DB_FILE" "$new_user" "$new_pass" <<'PY'
import hashlib, os, sqlite3, sys

db_path, new_user, new_pass = sys.argv[1], sys.argv[2], sys.argv[3]
con = sqlite3.connect(db_path)
cur = con.cursor()
cols = [r[1] for r in cur.execute("PRAGMA table_info(users)").fetchall()]
if not cols:
    print("ERR_NO_USERS_TABLE", file=sys.stderr)
    sys.exit(2)

# Generate salted sha256/bcrypt-compatible fallback or standard bcrypt via htpasswd/openssl/python
# Check if bcrypt CLI or python bcrypt is available, otherwise store salted sha256 marker handled by backend or bcrypt
hashed = ""
try:
    import bcrypt  # type: ignore
    hashed = bcrypt.hashpw(new_pass.encode("utf-8"), bcrypt.gensalt()).decode("utf-8")
except Exception:
    pass

if not hashed:
    # Try go / htpasswd / openssl if present
    import subprocess
    for cmd in [
        ["htpasswd", "-bnBC", "10", "", new_pass],
    ]:
        try:
            out = subprocess.check_output(cmd, stderr=subprocess.DEVNULL, text=True).strip()
            if ":" in out:
                hashed = out.split(":", 1)[1]
                break
        except Exception:
            pass

if not hashed:
    # Fallback: use a lightweight helper or sqlite update if a user already exists
    print("NO_BCRYPT_LIB", file=sys.stderr)
    sys.exit(3)

if "token_version" in cols:
    if new_user:
        cur.execute("UPDATE users SET username = ?, password_hash = ?, token_version = COALESCE(token_version, 1) + 1", (new_user, hashed))
    else:
        cur.execute("UPDATE users SET password_hash = ?, token_version = COALESCE(token_version, 1) + 1", (hashed,))
else:
    if new_user:
        cur.execute("UPDATE users SET username = ?, password_hash = ?", (new_user, hashed))
    else:
        cur.execute("UPDATE users SET password_hash = ?", (hashed,))

con.commit()
con.close()
PY
    local rc=$?
    if [[ $rc -eq 0 ]]; then
        systemctl restart "$SVC" 2>/dev/null || true
        echo -e "  ${G}✓ Panel credentials updated and service restarted.${NC}"
        return 0
    elif [[ $rc -eq 3 ]]; then
        # If python3 bcrypt isn't installed, generate bcrypt hash using a small temporary Go snippet if go is installed
        if command -v go >/dev/null 2>&1; then
            local tmp_go
            tmp_go=$(mktemp /tmp/mc_pw_XXXX.go)
            cat > "$tmp_go" <<'GOEOF'
package main
import (
	"fmt"
	"os"
	"golang.org/x/crypto/bcrypt"
)
func main() {
	if len(os.Args) < 2 { os.Exit(1) }
	h, err := bcrypt.GenerateFromPassword([]byte(os.Args[1]), bcrypt.DefaultCost)
	if err != nil { os.Exit(1) }
	fmt.Print(string(h))
}
GOEOF
            local bc_hash
            bc_hash=$(cd /opt/mcontrol && go run "$tmp_go" "$new_pass" 2>/dev/null || true)
            rm -f "$tmp_go"
            if [[ -n "$bc_hash" ]]; then
                python3 - "$DB_FILE" "$new_user" "$bc_hash" <<'PY'
import sqlite3, sys
db_path, new_user, hashed = sys.argv[1], sys.argv[2], sys.argv[3]
con = sqlite3.connect(db_path)
cur = con.cursor()
cols = [r[1] for r in cur.execute("PRAGMA table_info(users)").fetchall()]
if "token_version" in cols:
    if new_user:
        cur.execute("UPDATE users SET username = ?, password_hash = ?, token_version = COALESCE(token_version, 1) + 1", (new_user, hashed))
    else:
        cur.execute("UPDATE users SET password_hash = ?, token_version = COALESCE(token_version, 1) + 1", (hashed,))
else:
    if new_user:
        cur.execute("UPDATE users SET username = ?, password_hash = ?", (new_user, hashed))
    else:
        cur.execute("UPDATE users SET password_hash = ?", (hashed,))
con.commit()
con.close()
PY
                systemctl restart "$SVC" 2>/dev/null || true
                echo -e "  ${G}✓ Panel credentials updated and service restarted.${NC}"
                return 0
            fi
        fi
    fi
    echo -e "  ${R}✖ Could not generate bcrypt hash automatically; reset via Setup or Go tool.${NC}"
    return 1
}



mc_get_channel() {
    if [[ -f "$CHANNEL_FILE" ]]; then
        local ch
        ch=$(tr -d '[:space:]' < "$CHANNEL_FILE" 2>/dev/null || true)
        [[ "$ch" == "beta" ]] && { echo "beta"; return; }
        [[ "$ch" == "stable" ]] && { echo "stable"; return; }
    fi
    if [[ "$(db_get beta_builds)" == "1" ]]; then
        echo "beta"; return
    fi
    echo "stable"
}

mc_get_beta_build() {
    local bnum=""
    bnum=$(db_get installed_beta_build)
    if [[ -n "$bnum" && "$bnum" != "0" ]]; then
        echo "$bnum"; return
    fi
    if [[ -f "${DATA_DIR}/.beta_build" ]]; then
        bnum=$(tr -d '[:space:]' < "${DATA_DIR}/.beta_build" 2>/dev/null || true)
        [[ -n "$bnum" && "$bnum" != "0" ]] && { echo "$bnum"; return; }
    fi
    if [[ -f "${DATA_DIR}/beta-meta.json" ]]; then
        bnum=$(grep -oE '"build"[[:space:]]*:[[:space:]]*[0-9]+' "${DATA_DIR}/beta-meta.json" 2>/dev/null | grep -oE '[0-9]+' | head -n1 || true)
        [[ -n "$bnum" ]] && { echo "$bnum"; return; }
    fi
    echo ""
}

mc_get_mirror() {
    local m
    m=$(db_get panel_mirror_url)
    [[ -n "$m" ]] || m="${MCONTROL_MIRROR_BASE:-https://c107328.parspack.net/c107328/MControl}"
    echo "${m%/}"
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

    local port ch bnum ch_lbl ch_col
    port=$(mc_get_port)
    ch=$(mc_get_channel)
    bnum=$(mc_get_beta_build)
    if [[ "$ch" == "beta" ]]; then
        if [[ -n "$bnum" ]]; then
            ch_lbl="BETA #${bnum}"
        else
            ch_lbl="BETA"
        fi
        ch_col="$Y"
    else
        ch_lbl="STABLE"
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
    local bnum_ss
    bnum_ss=$(mc_get_beta_build)
    if [[ "$ch" == "beta" && -n "$bnum_ss" ]]; then
        printf "  ${FR}├─${NC} ${DIM}Channel        :${NC} ${Y}beta (Beta #%s)${NC}\n" "$bnum_ss"
    else
        printf "  ${FR}├─${NC} ${DIM}Channel        :${NC} ${Y}%s${NC}\n" "$ch"
    fi
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

mc_cli_uninstall() {
    echo ""
    printf "  ${R}⚠ Uninstall MControl and permanently delete ALL services, files & databases? [y/N]: ${NC}"
    read -r yn
    if [[ ! "$yn" =~ ^[Yy]$ ]]; then
        echo -e "  ${DIM}Uninstall cancelled.${NC}"
        return 0
    fi
    echo -e "  ${Y}● Stopping and disabling MControl services...${NC}"
    systemctl stop "$SVC" mcontrol-outbound.service 2>/dev/null || true
    systemctl disable "$SVC" mcontrol-outbound.service 2>/dev/null || true
    pkill -f "${INSTALL_DIR}/mcontrol" 2>/dev/null || true

    echo -e "  ${Y}● Removing systemd units, binaries, configs & databases...${NC}"
    rm -f /etc/systemd/system/mcontrol.service           /etc/systemd/system/mcontrol-outbound.service           /etc/systemd/system/multi-user.target.wants/mcontrol.service           /etc/systemd/system/multi-user.target.wants/mcontrol-outbound.service           "$CLI_BIN"           /usr/local/bin/mcontrol-menu
    rm -rf "$INSTALL_DIR" "$DATA_DIR" /etc/mcontrol /root/mcontrol
    systemctl daemon-reload 2>/dev/null || true
    systemctl reset-failed "$SVC" mcontrol-outbound.service 2>/dev/null || true

    if [[ ! -e "$INSTALL_DIR" && ! -e "$DATA_DIR" && ! -e /etc/systemd/system/mcontrol.service ]]; then
        echo -e "\n  ${G}✓ MControl, services, configs, and databases completely removed.${NC}\n"
    else
        echo -e "\n  ${Y}⚠ MControl removed (some paths may require manual check).${NC}\n"
    fi
    exit 0
}


update_mcontrol() {
    echo ""
    echo -e "  ${W}Update MControl Panel${NC}"
    echo -e "  ${DIM}Choose download source and channel:${NC}"
    echo -e "    ${G}[1]${NC} Stable — Iran Mirror (ParsPack)"
    echo -e "    ${C}[2]${NC} Stable — Foreign (GitHub)"
    echo -e "    ${Y}[3]${NC} Beta   — Iran Mirror (ParsPack)"
    echo -e "    ${M}[4]${NC} Beta   — Foreign (GitHub)"
    echo -e "    ${DIM}[0] Cancel${NC}"
    printf "  ${C}Select [1-4, 0] ❯❯ ${NC}"
    read -r uopt || return
    local arch base_url label
    arch=$(mc_arch)
    case "$uopt" in
        1) base_url="https://c107328.parspack.net/c107328/MControl"; label="Stable (Iran Mirror)" ;;
        2) base_url="https://github.com/htzserv/MControl/releases/latest/download"; label="Stable (GitHub)" ;;
        3) base_url="https://c107328.parspack.net/c107328/MControl/beta-latest"; label="Beta (Iran Mirror)" ;;
        4) base_url="https://github.com/htzserv/MControl/releases/download/beta-latest"; label="Beta (GitHub)" ;;
        0|"") return ;;
        *) echo -e "  ${R}✖ Invalid choice.${NC}"; return ;;
    esac
    echo -e "\n  ${C}Downloading MControl (${label}, linux-${arch})...${NC}"
    mkdir -p "$INSTALL_DIR"
    local tmp_bin="${BIN}.new"
    if ! curl -fSL --connect-timeout 10 --max-time 180 "${base_url}/mcontrol-linux-${arch}" -o "$tmp_bin"; then
        rm -f "$tmp_bin"
        echo -e "  ${R}✖ Download failed from ${label}.${NC}"
        return
    fi
    if [[ ! -s "$tmp_bin" ]]; then
        rm -f "$tmp_bin"
        echo -e "  ${R}✖ Downloaded binary is empty.${NC}"
        return
    fi
    chmod 755 "$tmp_bin"
    [[ -f "$BIN" ]] && cp -f "$BIN" "${BIN}.bak" 2>/dev/null || true
    mv -f "$tmp_bin" "$BIN"
    # Also refresh CLI wrapper script at /usr/local/bin/mcontrol
    local tmp_cli="${CLI_BIN}.new"
    if curl -fsSL --connect-timeout 6 --max-time 30 "${base_url}/mcontrol-cli.sh" -o "$tmp_cli" 2>/dev/null && [[ -s "$tmp_cli" ]]; then
        chmod 755 "$tmp_cli"
        mv -f "$tmp_cli" "$CLI_BIN"
    else
        rm -f "$tmp_cli" 2>/dev/null || true
    fi
    systemctl restart "$SVC"
    sleep 1
    if systemctl is-active --quiet "$SVC"; then
        echo -e "\n  ${G}✓ MControl updated (${label}) and service restarted successfully!${NC}"
    else
        echo -e "\n  ${R}✖ Service failed to start after update — restoring backup...${NC}"
        if [[ -f "${BIN}.bak" ]]; then
            mv -f "${BIN}.bak" "$BIN"
            systemctl restart "$SVC" || true
        fi
    fi
}

while true; do
    draw_header

    mc_group "PANEL SERVICE" first
    mc_row "1"  "Restart MControl Service" "$G"
    mc_row "2"  "Start MControl Service" "$C"
    mc_row "3"  "Stop MControl Service" "$Y"
    mc_row "4"  "View Service Logs (last 40 lines)" "$C"
    mc_row "5"  "Show Settings (Excl. User/Pass)" "$C" " ${DIM}(No Secrets)${NC}"

    mc_group "ACCESS & SECURITY"
    mc_row "6"  "Change Panel Port" "$M" " ${DIM}(SQLite + systemd)${NC}"
    mc_row "7"  "Change Secret Web Path" "$M"
    mc_row "8"  "Disable 2FA" "$Y" " ${DIM}(Emergency Reset)${NC}"
    mc_row "9"  "Disable HTTPS" "$Y" " ${DIM}(Switch back to HTTP)${NC}"

    mc_group "MAINTENANCE"
    mc_row "10" "Backup All Databases" "$B" " ${DIM}(/var/lib/mcontrol)${NC}"
    mc_row "11" "Reset Panel Username / Password" "$Y" " ${DIM}(SQLite Auth)${NC}"
    mc_row "12" "Reset Panel Settings (Domain / Path / 2FA)" "$Y" " ${DIM}(SQLite + TLS)${NC}"
    mc_row "13" "Update MControl (Stable / Beta)" "$G" " ${DIM}(Iran / GitHub)${NC}"
    mc_row "14" "Uninstall MControl" "$R"

    mc_footer "Exit"

    printf "  ${C}Select [1-14, 0] ❯❯ ${NC}"
    read -r opt || exit 0
    case "$opt" in
        1) systemctl restart "$SVC" && echo -e "\n  ${G}✓ Service restarted.${NC}" || echo -e "\n  ${R}✖ Restart failed.${NC}"; mc_pause ;;
        2) systemctl start "$SVC" && echo -e "\n  ${G}✓ Service started.${NC}" || echo -e "\n  ${R}✖ Start failed.${NC}"; mc_pause ;;
        3) systemctl stop "$SVC" && echo -e "\n  ${Y}● Service stopped.${NC}" || echo -e "\n  ${R}✖ Stop failed.${NC}"; mc_pause ;;
        4) echo ""; journalctl -u "$SVC" -n 40 --no-pager || true; mc_pause ;;
        5) show_settings; mc_pause ;;
        6)
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
        7)
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
        8)
            db_set twofa_enabled "0" && echo -e "\n  ${G}✓ 2FA disabled.${NC}" || echo -e "\n  ${R}✖ Database not found.${NC}"
            mc_pause ;;
        9)
            rm -f "$DATA_DIR/tls.crt" "$DATA_DIR/tls.key" && systemctl restart "$SVC"
            echo -e "\n  ${Y}● HTTPS certificates removed. Switched to HTTP.${NC}"
            mc_pause ;;
        10)
            out="/root/mcontrol-backup-$(date +%Y%m%d-%H%M%S).tar.gz"
            if [[ -d "$DATA_DIR" ]]; then
                tar -czf "$out" -C "$(dirname "$DATA_DIR")" "$(basename "$DATA_DIR")"
                echo -e "\n  ${G}✓ Backup created: ${W}${out}${NC}"
            else
                echo -e "\n  ${R}✖ ${DATA_DIR} not found.${NC}"
            fi
            mc_pause ;;
        11)
            reset_panel_credentials
            mc_pause ;;
        12)
            reset_panel_settings
            mc_pause ;;
        13)
            update_mcontrol
            mc_pause ;;
        14)
            mc_cli_uninstall
            mc_pause ;;
        0|q|Q) echo ""; exit 0 ;;
        *) echo -e "  ${R}✖ Invalid selection.${NC}"; sleep 1 ;;
    esac
done