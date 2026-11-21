#!/usr/bin/env bash
set -euo pipefail

SVC="mcontrol.service"
DATA_DIR="/var/lib/mcontrol"
DB_FILE="$DATA_DIR/mcontrol.db"
INSTALL_DIR="/opt/mcontrol"

C_CYAN="\033[1;36m"
C_GREEN="\033[1;32m"
C_YELLOW="\033[1;33m"
C_RED="\033[1;31m"
C_RESET="\033[0m"

get_ip() {
  local ip
  ip=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}')
  [ -n "$ip" ] || ip=$(hostname -I | awk '{print $1}')
  echo "${ip:-127.0.0.1}"
}

db_get() {
  local key="$1"
  [ -f "$DB_FILE" ] || { echo ""; return; }
  python3 - "$DB_FILE" "$key" <<'PY' 2>/dev/null || true
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
row = con.execute("SELECT value FROM settings WHERE key=?", (sys.argv[2],)).fetchone()
print(row[0] if row else "")
PY
}

db_set() {
  local key="$1" val="$2"
  [ -f "$DB_FILE" ] || return 1
  python3 - "$DB_FILE" "$key" "$val" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
con.execute("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", (sys.argv[2], sys.argv[3]))
con.commit()
PY
}

get_status_line() {
  if systemctl is-active --quiet "$SVC"; then
    echo -e "${C_GREEN}ACTIVE (Running)${C_RESET}"
  else
    echo -e "${C_RED}INACTIVE (Stopped)${C_RESET}"
  fi
}

show_info() {
  local ip port wpath scheme="http"
  ip=$(get_ip)
  port=$(db_get panel_port)
  [ -n "$port" ] || port=$(grep -oP '(?<=-port )\d+' /etc/systemd/system/mcontrol.service 2>/dev/null || echo "2053")
  wpath=$(db_get web_path)
  if [ -f "$DATA_DIR/tls.crt" ] && [ -f "$DATA_DIR/tls.key" ]; then scheme="https"; fi
  echo -e "${C_CYAN}────────────────────────────────────────────────────────${C_RESET}"
  echo -e "  Service Status : $(get_status_line)"
  echo -e "  Panel URL      : ${C_YELLOW}${scheme}://${ip}:${port}/${wpath:+${wpath}/}${C_RESET}"
  echo -e "  Active Port    : ${C_YELLOW}${port}${C_RESET}"
  echo -e "  Web Path       : ${C_YELLOW}/${wpath}${C_RESET}"
  echo -e "  HTTPS / TLS    : ${scheme^^}"
  echo -e "${C_CYAN}────────────────────────────────────────────────────────${C_RESET}"
}

while true; do
  clear 2>/dev/null || true
  echo -e "${C_CYAN}"
  echo "  ╔══════════════════════════════════════════════════════╗"
  echo "  ║           MControl CLI Manager (MDesign)             ║"
  echo "  ╚══════════════════════════════════════════════════════╝"
  echo -e "${C_RESET}"
  show_info
  echo "  1) Restart MControl Service"
  echo "  2) Start MControl Service"
  echo "  3) Stop MControl Service"
  echo "  4) View Live Service Logs (last 40 lines)"
  echo "  5) Change Panel Port (updates SQLite + systemd)"
  echo "  6) Change Secret Web Path"
  echo "  7) Disable 2FA (Emergency Reset)"
  echo "  8) Disable HTTPS (Switch back to HTTP)"
  echo "  9) Uninstall MControl"
  echo "  0) Exit"
  echo ""
  read -rp "  Select an option [0-9]: " opt
  case "$opt" in
    1) systemctl restart "$SVC" && echo -e "${C_GREEN}Service restarted.${C_RESET}"; sleep 1.2 ;;
    2) systemctl start "$SVC" && echo -e "${C_GREEN}Service started.${C_RESET}"; sleep 1.2 ;;
    3) systemctl stop "$SVC" && echo -e "${C_YELLOW}Service stopped.${C_RESET}"; sleep 1.2 ;;
    4) journalctl -u "$SVC" -n 40 --no-pager; read -rp "  Press Enter to continue..." _ ;;
    5)
      read -rp "  Enter new port (1-65535): " newp
      if [[ "$newp" =~ ^[0-9]+$ ]] && [ "$newp" -ge 1 ] && [ "$newp" -le 65535 ]; then
        db_set panel_port "$newp" || true
        sed -i -E "s/-port [0-9]+/-port ${newp}/" /etc/systemd/system/mcontrol.service
        systemctl daemon-reload
        systemctl restart "$SVC"
        echo -e "${C_GREEN}Panel port updated to ${newp} in SQLite & systemd.${C_RESET}"
      else
        echo -e "${C_RED}Invalid port.${C_RESET}"
      fi
      sleep 1.5
      ;;
    6)
      read -rp "  Enter new web path (3-48 chars, e.g. mc_panel1): " newwp
      newwp="${newwp#/}"; newwp="${newwp%/}"
      if [[ "$newwp" =~ ^[A-Za-z0-9_-]{3,48}$ ]]; then
        db_set web_path "$newwp" && systemctl restart "$SVC"
        echo -e "${C_GREEN}Web path updated to /${newwp}/ and service restarted.${C_RESET}"
      else
        echo -e "${C_RED}Invalid web path format.${C_RESET}"
      fi
      sleep 1.5
      ;;
    7)
      db_set twofa_enabled "0" && echo -e "${C_GREEN}2FA disabled.${C_RESET}"
      sleep 1.2
      ;;
    8)
      rm -f "$DATA_DIR/tls.crt" "$DATA_DIR/tls.key" && systemctl restart "$SVC"
      echo -e "${C_YELLOW}HTTPS certificates removed. Switched to HTTP.${C_RESET}"
      sleep 1.5
      ;;
    9)
      read -rp "  Are you sure you want to uninstall MControl? [y/N]: " yn
      if [[ "$yn" =~ ^[Yy]$ ]]; then
        systemctl disable --now "$SVC" 2>/dev/null || true
        rm -f /etc/systemd/system/mcontrol.service /usr/local/bin/mcontrol
        rm -rf "$INSTALL_DIR"
        systemctl daemon-reload
        echo -e "${C_YELLOW}MControl uninstalled (database kept at $DATA_DIR).${C_RESET}"
        exit 0
      fi
      ;;
    0) exit 0 ;;
    *) echo -e "${C_RED}Invalid option.${C_RESET}"; sleep 1 ;;
  esac
done
