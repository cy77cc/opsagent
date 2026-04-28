#!/usr/bin/env bash
set -euo pipefail

go run ./cmd/agent run --config ./configs/config.yaml
