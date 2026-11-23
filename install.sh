#!/usr/bin/env bash
set -euo pipefail

REPO="${MCONTROL_REPO:-htzserv/MControl}"
BIN="/usr/local/bin/mcontrol"
DATA_DIR="/var/lib/mcontrol"
SERVICE="mcontrol"

if [ "$(id -u)" -ne 0 ]; then
  echo "Please run as root (sudo bash install.sh)"
  exit 1
fi

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *) echo "Unsupported arch: $ARCH"; exit 1 ;;
esac

URL="https://github.com/${REPO}/releases/latest/download/mcontrol-linux-${GOARCH}"
echo "Downloading MControl (${GOARCH}) from ${URL}..."
curl -fsSL "$URL" -o "${BIN}.tmp"
chmod +x "${BIN}.tmp"
mv "${BIN}.tmp" "$BIN"

mkdir -p "$DATA_DIR"
chmod 700 "$DATA_DIR"

cat > "/etc/systemd/system/${SERVICE}.service" <<EOF
[Unit]
Description=MControl v2 Multi-Tenant Panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${BIN} -port 2053 -data ${DATA_DIR}
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE"
systemctl restart "$SERVICE"
echo "MControl v2 installed and running on port 2053."
