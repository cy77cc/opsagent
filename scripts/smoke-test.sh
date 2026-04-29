#!/usr/bin/env bash
# smoke-test.sh — Quick smoke test for OpsAgent.
# Runs: build, unit tests, vet, security scan, sandbox check, integration tests.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

APP_NAME="${APP_NAME:-opsagent}"

PASS=0
FAIL=0
SKIP=0

run_step() {
    local name="$1"
    shift
    echo "━━━ $name ━━━"
    if "$@"; then
        echo "  ✓ $name passed"
        PASS=$((PASS + 1))
    else
        echo "  ✗ $name failed"
        FAIL=$((FAIL + 1))
    fi
    echo ""
}

skip_step() {
    local name="$1"
    local reason="$2"
    echo "━━━ $name ━━━"
    echo "  ⊘ $name skipped: $reason"
    SKIP=$((SKIP + 1))
    echo ""
}

# 1. Build
run_step "Build" go build -o "bin/${APP_NAME}" ./cmd/agent

# 2. Unit tests
run_step "Unit Tests" go test ./... -race -count=1 -timeout 60s

# 3. Vet
run_step "Vet" go vet ./...

# 4. Security scan (optional — skip if gosec is not installed)
if command -v gosec >/dev/null 2>&1; then
    run_step "Security Scan" gosec ./...
else
    skip_step "Security Scan" "gosec not installed"
fi

# 5. Sandbox check
echo "━━━ Sandbox Check ━━━"
NSJAIL_OK=false
CGROUP_OK=false
NS_OK=false
if command -v nsjail >/dev/null 2>&1; then
    echo "  nsjail: OK"
    NSJAIL_OK=true
else
    echo "  nsjail: NOT FOUND"
fi
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    echo "  cgroup v2: OK"
    CGROUP_OK=true
else
    echo "  cgroup v2: NOT FOUND"
fi
if [ -d /proc/self/ns ]; then
    echo "  namespaces: OK"
    NS_OK=true
else
    echo "  namespaces: NOT FOUND"
fi
if $NSJAIL_OK && $CGROUP_OK && $NS_OK; then
    echo "  ✓ Sandbox Check passed"
    PASS=$((PASS + 1))
else
    echo "  ⊘ Sandbox Check skipped: missing prerequisites"
    SKIP=$((SKIP + 1))
fi
echo ""

# 6. Integration tests
run_step "Integration Tests" go test ./internal/integration/... -v -race -count=1 -timeout 60s

# Summary
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Passed:   $PASS"
echo "  Failed:   $FAIL"
echo "  Skipped:  $SKIP"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [ "$FAIL" -gt 0 ]; then
    echo "SMOKE TEST FAILED"
    exit 1
fi

echo "SMOKE TEST PASSED"
