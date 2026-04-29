#!/usr/bin/env bash
# ci-package.sh — Package a pre-built binary into a distributable tar.gz.
# Used by GitHub Actions release workflow and local packaging.
#
# Required env vars:
#   APP_NAME  — binary/package name (e.g. opsagent)
#   VERSION   — version string (e.g. v1.0.0)
#   ARCH      — goarch (amd64 or arm64)
#
# Expects binary at: dist/${ARCH}/${APP_NAME}
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

: "${APP_NAME:?APP_NAME is required}"
: "${VERSION:?VERSION is required}"
: "${ARCH:?ARCH is required}"

PKG_NAME="${APP_NAME}-${VERSION}-linux-${ARCH}"
PKG_DIR="dist/${ARCH}/${PKG_NAME}"

echo "Packaging ${PKG_NAME} ..."

# Create directory structure
mkdir -p "${PKG_DIR}/usr/local/bin"
mkdir -p "${PKG_DIR}/etc/${APP_NAME}"
mkdir -p "${PKG_DIR}/etc/systemd/system"
mkdir -p "${PKG_DIR}/var/log/${APP_NAME}"

# Binary
cp "dist/${ARCH}/${APP_NAME}" "${PKG_DIR}/usr/local/bin/${APP_NAME}"
chmod 755 "${PKG_DIR}/usr/local/bin/${APP_NAME}"

# Config
cp configs/config.yaml "${PKG_DIR}/etc/${APP_NAME}/config.yaml"

# Systemd service
cat > "${PKG_DIR}/etc/systemd/system/${APP_NAME}.service" <<EOF
[Unit]
Description=OpsAgent Host Agent
Documentation=https://github.com/cy77cc/opsagent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/${APP_NAME} run --config /etc/${APP_NAME}/config.yaml
Restart=always
RestartSec=5
LimitNOFILE=65536

# Security hardening
NoNewPrivileges=false
ProtectSystem=strict
ReadWritePaths=/var/log/${APP_NAME} /tmp/${APP_NAME}
ProtectHome=true
PrivateTmp=true

# Environment
Environment=LOG_LEVEL=info

[Install]
WantedBy=multi-user.target
EOF

# Install script
cat > "${PKG_DIR}/install.sh" <<'INSTALLEOF'
#!/usr/bin/env bash
set -euo pipefail

APP_NAME="opsagent"

if [[ $EUID -ne 0 ]]; then
    echo "Error: This script must be run as root (use sudo)"
    exit 1
fi

echo "Installing OpsAgent..."

mkdir -p /etc/${APP_NAME}
mkdir -p /var/log/${APP_NAME}
mkdir -p /tmp/${APP_NAME}/sandbox

install -m 755 usr/local/bin/${APP_NAME} /usr/local/bin/${APP_NAME}
echo "  ✓ Binary installed to /usr/local/bin/${APP_NAME}"

if [[ -f /etc/${APP_NAME}/config.yaml ]]; then
    echo "  ⊘ Config already exists (not overwritten)"
    echo "    New config saved as /etc/${APP_NAME}/config.yaml.new"
    cp etc/${APP_NAME}/config.yaml /etc/${APP_NAME}/config.yaml.new
else
    cp etc/${APP_NAME}/config.yaml /etc/${APP_NAME}/config.yaml
    echo "  ✓ Config installed to /etc/${APP_NAME}/config.yaml"
fi

cp etc/systemd/system/${APP_NAME}.service /etc/systemd/system/${APP_NAME}.service
systemctl daemon-reload
echo "  ✓ Systemd service installed"

echo ""
echo "Installation complete. Next steps:"
echo ""
echo "  1. Edit config:    sudo vim /etc/${APP_NAME}/config.yaml"
echo "  2. Start service:  sudo systemctl start ${APP_NAME}"
echo "  3. Enable on boot: sudo systemctl enable ${APP_NAME}"
echo "  4. Check status:   sudo systemctl status ${APP_NAME}"
echo "  5. View logs:      sudo journalctl -u ${APP_NAME} -f"
echo ""
echo "To uninstall: sudo ./uninstall.sh"
echo ""
INSTALLEOF
chmod 755 "${PKG_DIR}/install.sh"

# Uninstall script
cp scripts/uninstall.sh "${PKG_DIR}/uninstall.sh"
chmod 755 "${PKG_DIR}/uninstall.sh"

# Create tar.gz
tar -czf "dist/${PKG_NAME}.tar.gz" -C "dist/${ARCH}" "${PKG_NAME}"
echo "✓ dist/${PKG_NAME}.tar.gz"

# Cleanup staging directory
rm -rf "${PKG_DIR}"
