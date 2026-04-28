APP_NAME=nodeagentx
RUST_RUNTIME=nodeagentx-rust-runtime

.PHONY: tidy test run build rust-build proto-gen

tidy:
	go mod tidy

test:
	go test ./...

run:
	go run ./cmd/agent run --config ./configs/config.yaml

build:
	go build -o bin/$(APP_NAME) ./cmd/agent

rust-build:
	cd rust-runtime && cargo build --release

proto-gen:
	protoc --go_out=internal/grpcclient --go_opt=paths=source_relative \
		--go-grpc_out=internal/grpcclient --go-grpc_opt=paths=source_relative \
		proto/agent.proto
