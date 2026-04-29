APP_NAME=opsagent
RUST_RUNTIME=opsagent-rust-runtime
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: tidy test test-race test-cover build build-all run lint vet proto proto-gen \
       rust-build sandbox-check integration integration-sandbox security ci \
       package package-amd64 package-arm64 clean

## ── Go ────────────────────────────────────────────────────────────────────────

tidy:
	go mod tidy

test:
	go test ./... -count=1

test-race:
	go test ./... -race -count=1

test-cover:
	go test ./... -race -coverprofile=coverage.out -covermode=atomic -count=1
	@echo "Coverage report: coverage.out"
	@go tool cover -func=coverage.out | tail -1

build:
	go build -o bin/$(APP_NAME) ./cmd/agent

build-all:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/$(APP_NAME)-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o bin/$(APP_NAME)-arm64 ./cmd/agent
	@echo "Built: bin/$(APP_NAME)-amd64 bin/$(APP_NAME)-arm64"

run:
	go run ./cmd/agent run --config ./configs/config.yaml

## ── Linting & Static Analysis ─────────────────────────────────────────────────

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed"; exit 1; }
	golangci-lint run ./...

vet:
	go vet ./...

## ── Proto ─────────────────────────────────────────────────────────────────────

proto: proto-gen

proto-gen:
	protoc --go_out=internal/grpcclient --go_opt=paths=source_relative \
		--go-grpc_out=internal/grpcclient --go-grpc_opt=paths=source_relative \
		proto/agent.proto

## ── Rust Runtime ──────────────────────────────────────────────────────────────

rust-build:
	cd rust-runtime && cargo build --release

## ── Sandbox ───────────────────────────────────────────────────────────────────

sandbox-check:
	@echo "Checking sandbox prerequisites..."
	@command -v nsjail >/dev/null 2>&1 && echo "  nsjail: OK" || echo "  nsjail: NOT FOUND"
	@test -f /sys/fs/cgroup/cgroup.controllers && echo "  cgroup v2: OK" || echo "  cgroup v2: NOT FOUND"
	@test -d /proc/self/ns && echo "  namespaces: OK" || echo "  namespaces: NOT FOUND"

## ── Integration Tests ─────────────────────────────────────────────────────────

integration:
	go test ./internal/integration/... -v -race -count=1 -timeout 60s

integration-sandbox:
	@command -v nsjail >/dev/null 2>&1 || { echo "nsjail not installed, skipping"; exit 0; }
	sudo go test ./internal/integration/... -v -run TestSandbox -race -count=1 -timeout 60s

## ── Security ──────────────────────────────────────────────────────────────────

security:
	@command -v gosec >/dev/null 2>&1 || { echo "gosec not installed"; exit 1; }
	gosec ./...

## ── Packaging ─────────────────────────────────────────────────────────────────

package:
	VERSION=$(VERSION) ./scripts/package.sh

package-amd64:
	VERSION=$(VERSION) ARCHITECTURES=amd64 ./scripts/package.sh

package-arm64:
	VERSION=$(VERSION) ARCHITECTURES=arm64 ./scripts/package.sh

clean:
	rm -rf bin/ dist/ coverage.out

## ── CI Pipeline ───────────────────────────────────────────────────────────────

ci: tidy vet test-race security
	@echo "CI pipeline completed successfully"
