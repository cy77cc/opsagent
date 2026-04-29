#!/usr/bin/env bash
# uninstall.sh — Uninstall NodeAgentX from the system.
set -euo pipefail

APP_NAME="nodeagentx"

if [[ $EUID -ne 0 ]]; then
    echo "Error: This script must be run as root (use sudo)"
    exit 1
fi

echo "Uninstalling NodeAgentX..."

# Stop and disable service
if systemctl is-active --quiet "${APP_NAME}" 2>/dev/null; then
    systemctl stop "${APP_NAME}"
    echo "  ✓ Service stopped"
fi

if systemctl is-enabled --quiet "${APP_NAME}" 2>/dev/null; then
    systemctl disable "${APP_NAME}"
    echo "  ✓ Service disabled"
fi

# Remove systemd service file
if [[ -f /etc/systemd/system/${APP_NAME}.service ]]; then
    rm -f /etc/systemd/system/${APP_NAME}.service
    systemctl daemon-reload
    echo "  ✓ Systemd service removed"
fi

# Remove binary
if [[ -f /usr/local/bin/${APP_NAME} ]]; then
    rm -f "/usr/local/bin/${APP_NAME}"
    echo "  ✓ Binary removed: /usr/local/bin/${APP_NAME}"
fi

# Ask before removing config
if [[ -d /etc/${APP_NAME} ]]; then
    read -rp "  Remove config directory /etc/${APP_NAME}? [y/N] " answer
    if [[ "${answer}" =~ ^[Yy]$ ]]; then
        rm -rf "/etc/${APP_NAME}"
        echo "  ✓ Config removed: /etc/${APP_NAME}"
    else
        echo "  ⊘ Config preserved: /etc/${APP_NAME}"
    fi
fi

# Ask before removing logs
if [[ -d /var/log/${APP_NAME} ]]; then
    read -rp "  Remove log directory /var/log/${APP_NAME}? [y/N] " answer
    if [[ "${answer}" =~ ^[Yy]$ ]]; then
        rm -rf "/var/log/${APP_NAME}"
        echo "  ✓ Logs removed: /var/log/${APP_NAME}"
    else
        echo "  ⊘ Logs preserved: /var/log/${APP_NAME}"
    fi
fi

# Remove temp directory
if [[ -d /tmp/${APP_NAME} ]]; then
    rm -rf "/tmp/${APP_NAME}"
    echo "  ✓ Temp removed: /tmp/${APP_NAME}"
fi

echo ""
echo "NodeAgentX uninstalled."
echo ""
echo "  Remaining items to check:"
echo "    - /etc/${APP_NAME}/config.yaml (if preserved)"
echo "    - /var/log/${APP_NAME}/ (if preserved)"
echo "    - nsjail/cgroup leftovers (if sandbox was enabled)"
