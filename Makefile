# Lang-Ango: eBPF-based Observability Agent
# Makefile

.PHONY: all build bpf test clean docker help generate lint vet

# Variables
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GO := go
CLANG := clang
LLVM_STRIP := llvm-strip
BPF_CLANG := clang
BPF_CFLAGS := -g -O2 -target bpf -D__TARGET_ARCH_x86
BPF_HEADERS := /usr/include
GOOS := $(shell $(GO) env GOOS)
GOARCH := $(shell $(GO) env GOARCH)

# Directories
BIN_DIR := bin
BPF_DIR := bpf
BPF_BIN_DIR := $(BPF_DIR)/bin
PKG_DIR := pkg

# eBPF source files
BPF_SOURCES := $(wildcard $(BPF_DIR)/kernels/**/*.c)
BPF_OBJECTS := $(BPF_SOURCES:$(BPF_DIR)/kernels/%.c=$(BPF_BIN_DIR)/%.o)

# Default target
all: help

help:
	@echo "Lang-Ango: eBPF-based Observability Agent"
	@echo ""
	@echo "Available targets:"
	@echo "  build          - Build the main agent binary"
	@echo "  bpf            - Compile eBPF programs"
	@echo "  all            - Build everything (bpf + agent)"
	@echo "  test           - Run unit tests"
	@echo "  integration    - Run integration tests (requires root)"
	@echo "  clean          - Clean build artifacts"
	@echo "  docker         - Build Docker image"
	@echo "  lint           - Run linters"
	@echo "  generate       - Generate code"

# Build the agent
build:
	@echo "Building lang-ango..."
	$(GO) build -ldflags="-s -w -X main.Version=$(VERSION) -X main.Build=$(BUILD)" \
		-o $(BIN_DIR)/lang-ango ./cmd/lang-ango
	@echo "Built: $(BIN_DIR)/lang-ango"

# Compile eBPF programs
bpf: $(BPF_OBJECTS)
	@echo "eBPF programs compiled"

$(BPF_BIN_DIR)/%.o: $(BPF_DIR)/kernels/%.c
	@mkdir -p $(dir $@)
	$(BPF_CLANG) $(BPF_CFLAGS) \
		-I$(BPF_DIR)/headers \
		-I$(BPF_DIR)/kernels/common \
		-c $< -o $@

# Clean eBPF artifacts
clean-bpf:
	rm -rf $(BPF_BIN_DIR)

# Clean everything
clean: clean-bpf
	rm -rf $(BIN_DIR)
	$(GO) clean

# All: build bpf and agent
all: bpf build

# Test
test:
	$(GO) test -v -race ./...

# Integration tests
integration:
	sudo $(GO) test -v -tags=integration ./...

# Docker
docker:
	docker build -t lang-ango:latest -f deployments/docker/Dockerfile .

# Lint
lint:
	$(GO) vet ./...
	golangci-lint run ./...

# Generate
generate:
	$(GO) generate ./...

# Install dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

# Development setup
dev: deps bpf build

# Run the agent
run:
	sudo $(BIN_DIR)/lang-ango

# Kubernetes deployment
k8s-deploy:
	kubectl apply -f deployments/kubernetes/
