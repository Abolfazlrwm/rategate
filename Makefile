# RateGate Makefile — build, test, bench, lint, proto
SHELL := /bin/bash
GO ?= go
PKG := github.com/Abolfazlrwm/rategate
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo v0.1.0)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT) -X $(PKG)/internal/version.BuildDate=$(BUILD_DATE)

PROTOC_GEN_GO     := $(shell which protoc-gen-go 2>/dev/null)
PROTOC_GEN_GO_GRPC := $(shell which protoc-gen-go-grpc 2>/dev/null)

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

.PHONY: build
build: ## Build all binaries into ./bin
	@mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/rategate-server ./cmd/rategate-server
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/rategate-bench  ./cmd/rategate-bench

.PHONY: test
test: ## Run unit tests
	$(GO) test ./... -count=1

.PHONY: test-race
test-race: ## Run tests with race detector
	$(GO) test -race -count=1 ./...

.PHONY: test-cover
test-cover: ## Run tests with coverage report
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out | tail -1
	$(GO) tool cover -html=coverage.out -o coverage.html || true

.PHONY: bench
bench: ## Run benchmarks
	$(GO) test -bench=. -benchmem -run=^$$ ./internal/algorithms/ ./internal/store/
	@echo "--- Benchmark results saved to benchmarks/"
	$(GO) test -bench=. -benchmem -run=^$$ ./internal/algorithms/ ./internal/store/ | tee benchmarks/latest.txt

.PHONY: bench-cpu
bench-cpu: ## Run benchmarks with CPU profiling
	$(GO) test -bench=. -benchmem -cpuprofile=profile.out -run=^$$ ./internal/algorithms/

.PHONY: lint
lint: ## Run golangci-lint
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2; \
	fi
	golangci-lint run ./...

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## go fmt
	$(GO) fmt ./...
	@if command -v goimports >/dev/null 2>&1; then goimports -w .; fi

.PHONY: proto
proto: ## Generate gRPC/protobuf code from api/proto/*.proto
	@if [ -z "$(PROTOC_GEN_GO)" ] || [ -z "$(PROTOC_GEN_GO_GRPC)" ]; then \
		echo "Installing protoc plugins..."; \
		go install google.golang.org/protobuf/cmd/protoc-gen-go@latest; \
		go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest; \
	fi
	protoc --go_out=. --go_opt=module=$(PKG) \
		--go-grpc_out=. --go-grpc_opt=module=$(PKG) \
		api/proto/rategate.proto

.PHONY: docker
docker: ## Build docker image
	docker build -t rategate:$(VERSION) -f deployments/docker/Dockerfile .

.PHONY: run
run: build ## Run server locally
	./bin/rategate-server

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin dist coverage.out coverage.html profile.out benchmarks/latest.txt

.PHONY: check
check: fmt vet test-race lint ## Run the full pre-commit gate: fmt + vet + test -race + lint
	@echo "All checks passed."

.PHONY: install
install: ## Install binaries to GOBIN
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/rategate-server
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/rategate-bench
