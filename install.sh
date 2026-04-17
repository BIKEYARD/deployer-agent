#!/usr/bin/env bash
# Compiles deployer-agent for the current platform, installs it,
# registers a system service, and starts it.
#
# Usage:  sudo ./install.sh
#
# Env overrides:
#   INSTALL_DIR   default: /opt/deployer-agent
#   SERVICE_NAME  default: deployer-agent
#   SERVICE_USER  default: root    (Linux only)

set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/deployer-agent}"
SERVICE_NAME="${SERVICE_NAME:-deployer-agent}"
SERVICE_USER="${SERVICE_USER:-root}"
BINARY_NAME="deployer-agent"

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OS="$(uname -s)"

need() { command -v "$1" >/dev/null 2>&1 || { echo "Required tool missing: $1" >&2; exit 1; }; }
need go

if [[ $EUID -ne 0 ]]; then
  echo "This script must be run as root (use sudo)." >&2
  exit 1
fi

echo ">> Building $BINARY_NAME from $SRC_DIR"
cd "$SRC_DIR"
CGO_ENABLED=0 go build -ldflags="-s -w" -o "$BINARY_NAME" .

echo ">> Installing to $INSTALL_DIR"
mkdir -p "$INSTALL_DIR"
install -m 0755 "$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"

if [[ ! -f "$INSTALL_DIR/config.yaml" ]]; then
  if [[ -f "$SRC_DIR/config.yaml" ]]; then
    install -m 0640 "$SRC_DIR/config.yaml" "$INSTALL_DIR/config.yaml"
  else
    install -m 0640 "$SRC_DIR/config.example.yaml" "$INSTALL_DIR/config.yaml"
    echo "!! Installed config.example.yaml as config.yaml — edit $INSTALL_DIR/config.yaml before production use."
  fi
fi

case "$OS" in
  Linux)
    need systemctl
    UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
    echo ">> Writing $UNIT_FILE"
    cat > "$UNIT_FILE" <<EOF
[Unit]
Description=Deployer Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/${BINARY_NAME} -config ${INSTALL_DIR}/config.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable "${SERVICE_NAME}.service"
    systemctl restart "${SERVICE_NAME}.service"
    echo ">> Service status:"
    systemctl --no-pager --full status "${SERVICE_NAME}.service" || true
    ;;

  Darwin)
    PLIST="/Library/LaunchDaemons/com.${SERVICE_NAME}.plist"
    echo ">> Writing $PLIST"
    cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.${SERVICE_NAME}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${INSTALL_DIR}/${BINARY_NAME}</string>
    <string>-config</string>
    <string>${INSTALL_DIR}/config.yaml</string>
  </array>
  <key>WorkingDirectory</key><string>${INSTALL_DIR}</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>${INSTALL_DIR}/agent.out.log</string>
  <key>StandardErrorPath</key><string>${INSTALL_DIR}/agent.err.log</string>
</dict>
</plist>
EOF
    chmod 0644 "$PLIST"
    launchctl unload "$PLIST" 2>/dev/null || true
    launchctl load -w "$PLIST"
    echo ">> Loaded launchd job com.${SERVICE_NAME}"
    ;;

  *)
    echo "Unsupported OS: $OS (only Linux/systemd and macOS/launchd are supported)." >&2
    exit 1
    ;;
esac

echo ">> Done. Binary: $INSTALL_DIR/$BINARY_NAME  Config: $INSTALL_DIR/config.yaml"
