#!/usr/bin/env bash
# package.sh — Cross-compile and package NodeAgentX for x86_64 and arm64.
# Produces: dist/nodeagentx-<version>-linux-<arch>.tar.gz
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

export APP_NAME="${APP_NAME:-nodeagentx}"
export VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"

# Support single-arch builds via ARCHITECTURES=amd64 env var
if [[ -n "${ARCHITECTURES:-}" ]]; then
    GOARCHES=("${ARCHITECTURES}")
else
    GOARCHES=("amd64" "arm64")
fi

LDFLAGS="-s -w"

echo "NodeAgentX Packager — version: ${VERSION}"
echo ""

mkdir -p dist

for arch in "${GOARCHES[@]}"; do
    echo "━━━ Building ${arch} ━━━"
    CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" \
        go build -trimpath -ldflags="${LDFLAGS}" \
        -o "dist/${arch}/${APP_NAME}" ./cmd/agent
    echo "  ✓ dist/${arch}/${APP_NAME}"

    echo "━━━ Packaging ${arch} ━━━"
    export ARCH="${arch}"
    bash scripts/ci-package.sh
    echo ""
done

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Packages:"
ls -lh dist/*.tar.gz 2>/dev/null || echo "  (none)"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Install: tar xzf <package>.tar.gz && cd <dir> && sudo ./install.sh"
