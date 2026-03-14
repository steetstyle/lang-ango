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
BPF_SOURCES := $(shell find $(BPF_DIR)/kernels -name '*.bpf.c')
BPF_OBJECTS := $(foreach src,$(BPF_SOURCES),$(BPF_BIN_DIR)/$(patsubst %.bpf.c,%.o,$(notdir $(src))))

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

define BPF_RULE
$(BPF_BIN_DIR)/$(patsubst %.bpf.c,%.o,$(notdir $(1))): $(1)
	@mkdir -p $(BPF_BIN_DIR)
	$(BPF_CLANG) $(BPF_CFLAGS) \
		-I$(BPF_DIR)/headers \
		-I$(BPF_DIR)/kernels/common \
		-c $$< -o $$@

endef

$(foreach src,$(BPF_SOURCES),$(eval $(call BPF_RULE,$(src))))

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

# ============================================
# Docker Compose and E2E Testing
# ============================================

.PHONY: docker-up docker-down docker-logs run-all stop test-curl test-traces

docker-up:
	@echo "Starting with Docker Compose..."
	docker-compose up --build -d

docker-down:
	@echo "Stopping Docker Compose..."
	docker-compose down

docker-logs:
	docker-compose logs -f

# run-all: Start Jaeger, Agent, and Sample API locally
run-all: run-jaeger run-agent run-sampleapi
	@echo ""
	@echo "==========================================="
	@echo "All services started!"
	@echo "==========================================="
	@echo "Jaeger UI:     http://localhost:16686"
	@echo "Sample API:    http://localhost:5002"
	@echo "OTLP gRPC:     localhost:4317"
	@echo "==========================================="

run-jaeger:
	@echo "Starting Jaeger..."
	@docker rm -f lang-ango-jaeger 2>/dev/null || true
	docker run -d --name lang-ango-jaeger \
		-p 16686:16686 \
		-p 4317:4317 \
		-p 4318:4318 \
		jaegertracing/all-in-one:latest
	@sleep 2

run-agent: build
	@echo "Starting Go agent..."
	@pkill -f "bin/lang-ango" 2>/dev/null || true
	@sleep 1
	OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 \
	OTEL_EXPORTER_OTLP_PROTOCOL=grpc \
	./bin/lang-ango &

run-sampleapi:
	@echo "Starting Sample API..."
	@pkill -f "SampleApi" 2>/dev/null || true
	@sleep 1
	cd test/load/SampleApi && \
	DOTNET_STARTUP_HOOKS=$(PWD)/dotnet/startup-hook/LangAngo.StartupHook/bin/Release/net9.0/langango.dll \
	dotnet bin/Release/net9.0/SampleApi.dll --urls "http://localhost:5002" &

stop:
	@echo "Stopping all services..."
	@pkill -f "bin/lang-ango" 2>/dev/null || true
	@pkill -f "SampleApi" 2>/dev/null || true
	@docker rm -f lang-ango-jaeger 2>/dev/null || true
	@echo "All services stopped."

test-curl:
	@echo "Sending test request..."
	@curl -s http://localhost:5002/api/data || echo "API not responding"

test-traces:
	@echo "Checking Jaeger for recent traces..."
	@curl -s 'http://localhost:16686/api/traces?service=lang-ango&limit=5&lookback=60s' | python3 -c \
		"import sys,json; d=json.load(sys.stdin); print(f'{len(d[\"data\"])} traces found')"
