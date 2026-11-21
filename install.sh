#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root: sudo bash install.sh [port]" >&2
  exit 1
fi

PORT="${1:-2053}"
INSTALL_DIR="/opt/mcontrol"
DATA_DIR="/var/lib/mcontrol"
REPO="${MCONTROL_REPO:-htzserv/MControl}"
RELEASE_BASE="${MCONTROL_RELEASE_BASE:-https://github.com/${REPO}/releases/latest/download}"

ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
  aarch64|arm64) ARCH="arm64" ;;
  *) ARCH="amd64" ;;
esac

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || pwd)"
BIN=""
CLI_SRC=""
VERSION_TXT=""

# 1) Check local pre-built binary next to install.sh
for cand in "$SCRIPT_DIR/mcontrol-linux-${ARCH}" "$SCRIPT_DIR/mcontrol-linux-amd64" "$SCRIPT_DIR/mcontrol"; do
  if [ -f "$cand" ] && head -c4 "$cand" 2>/dev/null | grep -q 'ELF'; then
    BIN="$cand"
    break
  fi
done
[ -f "$SCRIPT_DIR/mcontrol-cli.sh" ] && CLI_SRC="$SCRIPT_DIR/mcontrol-cli.sh"
[ -f "$SCRIPT_DIR/VERSION" ] && VERSION_TXT="$(tr -d '[:space:]' < "$SCRIPT_DIR/VERSION")"

TMPD=""
cleanup() { [ -n "$TMPD" ] && rm -rf "$TMPD"; }
trap cleanup EXIT

if [ -n "$BIN" ]; then
  echo "[1/3] Using local pre-built binary: $BIN"
elif [ -f "$SCRIPT_DIR/go.mod" ]; then
  echo "[1/3] Building MControl single binary from local source..."
  cd "$SCRIPT_DIR"
  command -v go >/dev/null 2>&1 || export PATH="$PATH:/usr/local/go/bin"
  command -v go >/dev/null 2>&1 || { echo "Error: Go 1.22+ is required to build from source." >&2; exit 1; }
  go mod tidy
  CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o mcontrol .
  BIN="$SCRIPT_DIR/mcontrol"
else
  echo "[1/3] Downloading pre-built MControl binary (${ARCH}) from GitHub Releases..."
  TMPD="$(mktemp -d)"
  BIN_NAME="mcontrol-linux-${ARCH}"
  curl -fsSL "${RELEASE_BASE}/${BIN_NAME}" -o "${TMPD}/${BIN_NAME}"
  if curl -fsSL "${RELEASE_BASE}/${BIN_NAME}.sha256" -o "${TMPD}/${BIN_NAME}.sha256" 2>/dev/null; then
    (cd "$TMPD" && sha256sum -c "${BIN_NAME}.sha256")
  fi
  BIN="${TMPD}/${BIN_NAME}"
  if curl -fsSL "${RELEASE_BASE}/mcontrol-cli.sh" -o "${TMPD}/mcontrol-cli.sh" 2>/dev/null; then
    CLI_SRC="${TMPD}/mcontrol-cli.sh"
  fi
  VERSION_TXT="$(curl -fsSL "${RELEASE_BASE}/VERSION" 2>/dev/null | tr -d '[:space:]' || echo "latest")"
fi

[ -n "$VERSION_TXT" ] || VERSION_TXT="1.1.8"

echo "[2/3] Installing binary to $INSTALL_DIR..."
mkdir -p "$INSTALL_DIR" "$DATA_DIR"
chmod 700 "$DATA_DIR"
install -m 0755 "$BIN" "$INSTALL_DIR/mcontrol"
if [ -n "$CLI_SRC" ] && [ -f "$CLI_SRC" ]; then
  install -m 0755 "$CLI_SRC" /usr/local/bin/mcontrol
fi

echo "[3/3] Creating systemd service (mcontrol.service)..."
ENV_LINE=""
if [ -n "${GOPROXY:-}" ]; then ENV_LINE="Environment=GOPROXY=${GOPROXY}"; fi
cat <<UNIT > /etc/systemd/system/mcontrol.service
[Unit]
Description=MControl Central Panel (MDesign)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/mcontrol -port $PORT -data $DATA_DIR
Restart=always
RestartSec=2
${ENV_LINE}

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable mcontrol.service
systemctl restart mcontrol.service

PUB_IP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -n1)
[ -n "$PUB_IP" ] || PUB_IP=$(hostname -I | awk '{print $1}')

TOKEN=""
for _ in 1 2 3 4 5 6 7 8; do
  TOKEN=$(journalctl -u mcontrol.service -n 50 --no-pager 2>/dev/null | grep -o 'FIRST-TIME SETUP TOKEN: [0-9a-f]*' | tail -n1 | awk '{print $4}' || true)
  [ -n "$TOKEN" ] && break
  sleep 1
done

echo ""
echo "  MControl v${VERSION_TXT} installed (MDesign)"
echo "  CLI Manager : run 'mcontrol' in terminal anytime"
echo "  Setup URL   : http://${PUB_IP}:${PORT}/"
if [ -n "$TOKEN" ]; then
  echo "  Setup token : ${TOKEN}   (asked once on the setup screen)"
else
  echo "  Setup token : already configured, or run: mcontrol"
fi
echo ""
