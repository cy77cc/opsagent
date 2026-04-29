#!/usr/bin/env bash
# package.sh — Cross-compile and package NodeAgentX for x86_64 and arm64.
# Produces: dist/nodeagentx-<version>-<arch>.tar.gz
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

APP_NAME="nodeagentx"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
DIST_DIR="dist"
LDFLAGS="-s -w -X main.version=${VERSION}"

# Support single-arch builds via ARCHITECTURES=amd64 env var
if [[ -n "${ARCHITECTURES:-}" ]]; then
    ARCHITECTURES=("${ARCHITECTURES}")
else
    ARCHITECTURES=(
        "amd64"
        "arm64"
    )
fi

build_arch() {
    local goarch="$1"
    local bin_name="${APP_NAME}"
    if [[ "$goarch" == "arm64" ]]; then
        bin_name="${APP_NAME}-arm64"
    fi

    echo "━━━ Building ${goarch} ━━━"
    CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" \
        go build -trimpath -ldflags="${LDFLAGS}" \
        -o "${DIST_DIR}/${goarch}/${bin_name}" ./cmd/agent
    echo "  ✓ ${goarch} binary: ${DIST_DIR}/${goarch}/${bin_name}"
}

package_arch() {
    local goarch="$1"
    local pkg_name="${APP_NAME}-${VERSION}-linux-${goarch}"
    local pkg_dir="${DIST_DIR}/${goarch}/${pkg_name}"

    echo "━━━ Packaging ${goarch} ━━━"

    # Create package directory structure
    mkdir -p "${pkg_dir}/usr/local/bin"
    mkdir -p "${pkg_dir}/etc/${APP_NAME}"
    mkdir -p "${pkg_dir}/etc/systemd/system"
    mkdir -p "${pkg_dir}/var/log/${APP_NAME}"

    # Copy binary
    local bin_name="${APP_NAME}"
    if [[ "$goarch" == "arm64" ]]; then
        bin_name="${APP_NAME}-arm64"
    fi
    cp "${DIST_DIR}/${goarch}/${bin_name}" "${pkg_dir}/usr/local/bin/${APP_NAME}"
    chmod 755 "${pkg_dir}/usr/local/bin/${APP_NAME}"

    # Copy config
    cp configs/config.yaml "${pkg_dir}/etc/${APP_NAME}/config.yaml"

    # Copy systemd service
    cat > "${pkg_dir}/etc/systemd/system/${APP_NAME}.service" <<'SERVICEEOF'
[Unit]
Description=NodeAgentX Host Agent
Documentation=https://github.com/your-org/NodeAgentX
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/nodeagentx run --config /etc/nodeagentx/config.yaml
Restart=always
RestartSec=5
LimitNOFILE=65536

# Security hardening
NoNewPrivileges=false
ProtectSystem=strict
ReadWritePaths=/var/log/nodeagentx /tmp/nodeagentx
ProtectHome=true
PrivateTmp=true

# Environment
Environment=LOG_LEVEL=info

[Install]
WantedBy=multi-user.target
SERVICEEOF

    # Copy install script
    cat > "${pkg_dir}/install.sh" <<'INSTALLEOF'
#!/usr/bin/env bash
# install.sh — Install NodeAgentX from extracted package.
set -euo pipefail

APP_NAME="nodeagentx"

if [[ $EUID -ne 0 ]]; then
    echo "Error: This script must be run as root (use sudo)"
    exit 1
fi

echo "Installing NodeAgentX..."

# Create directories
mkdir -p /etc/${APP_NAME}
mkdir -p /var/log/${APP_NAME}
mkdir -p /tmp/${APP_NAME}/sandbox

# Install binary
install -m 755 usr/local/bin/${APP_NAME} /usr/local/bin/${APP_NAME}
echo "  ✓ Binary installed to /usr/local/bin/${APP_NAME}"

# Install config (don't overwrite existing)
if [[ -f /etc/${APP_NAME}/config.yaml ]]; then
    echo "  ⊘ Config already exists at /etc/${APP_NAME}/config.yaml (not overwritten)"
    echo "    New config saved as /etc/${APP_NAME}/config.yaml.new"
    cp etc/${APP_NAME}/config.yaml /etc/${APP_NAME}/config.yaml.new
else
    cp etc/${APP_NAME}/config.yaml /etc/${APP_NAME}/config.yaml
    echo "  ✓ Config installed to /etc/${APP_NAME}/config.yaml"
fi

# Install systemd service
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
    chmod 755 "${pkg_dir}/install.sh"

    # Copy uninstall script
    cp scripts/uninstall.sh "${pkg_dir}/uninstall.sh"
    chmod 755 "${pkg_dir}/uninstall.sh"

    # Create tar.gz
    tar -czf "${DIST_DIR}/${pkg_name}.tar.gz" -C "${DIST_DIR}/${goarch}" "${pkg_name}"
    echo "  ✓ Package: ${DIST_DIR}/${pkg_name}.tar.gz"

    # Cleanup staging directory
    rm -rf "${pkg_dir}"

    echo ""
}

# ── Main ─────────────────────────────────────────────────────────────────────

echo "NodeAgentX Packager — version: ${VERSION}"
echo ""

mkdir -p "${DIST_DIR}"

for arch in "${ARCHITECTURES[@]}"; do
    build_arch "${arch}"
    package_arch "${arch}"
done

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Packages:"
ls -lh "${DIST_DIR}"/*.tar.gz
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Install: tar xzf <package>.tar.gz && cd <dir> && sudo ./install.sh"
