# Lang-Ango

eBPF-based Zero-Code OpenTelemetry Auto-Instrumentation Agent

## Overview

Lang-Ango is an eBPF-based observability agent that provides automatic instrumentation for distributed applications without requiring code changes. It captures network traffic at the kernel level and produces OpenTelemetry-compatible metrics and traces.

## Features

- **Zero-Code Instrumentation**: No code changes required
- **Multi-Protocol Support**: HTTP, gRPC, PostgreSQL, MySQL, Redis, Kafka
- **TLS Interception**: Transparent capture of encrypted traffic
- **Distributed Tracing**: W3C Trace Context propagation
- **Kubernetes Integration**: Pod/Service metadata enrichment
- **OpenTelemetry Export**: OTLP and Prometheus compatibility
- **Language Agnostic**: Works with any language (Go, Java, Python, Node.js, etc.)

## Architecture

```
┌─────────────────────────────────────────────┐
│           Kubernetes Cluster                 │
│  ┌─────────────────────────────────────┐   │
│  │         Lang-Ango DaemonSet         │   │
│  │  ┌─────────┐  ┌─────────┐          │   │
│  │  │ eBPF    │  │  User   │          │   │
│  │  │ Kernel  │◄─┤  Space  │◄──►OTel  │   │
│  │  │ Programs│  │  Agent  │   Export │   │
│  │  └─────────┘  └─────────┘          │   │
│  └─────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
```

## Quick Start

### Prerequisites

- Linux kernel 5.8+ with BTF enabled
- Go 1.21+
- clang/llvm
- Root/sudo access (for eBPF)

### Build

```bash
# Install dependencies
make deps

# Build eBPF programs
make bpf

# Build agent
make build

# Or build everything at once
make all
```

### Run

```bash
# With config file
sudo ./bin/lang-ango -config config.yaml

# With specific port
sudo ./bin/lang-ango -port 8080

# With specific PID
sudo ./bin/lang-ango -pid 12345
```

### Docker

```bash
# Build image
make docker

# Run in Docker
docker run -d \
  --privileged \
  -v /sys/kernel/debug:/sys/kernel/debug \
  -v /sys/kernel/btf:/sys/kernel/btf:ro \
  lang-ango:latest
```

### Kubernetes

```bash
# Deploy
kubectl apply -f deployments/kubernetes/
```

## Configuration

See `config.yaml` for all available options:

```yaml
service:
  name: lang-ango
  version: "0.1.0"
  
discovery:
  ports: [80, 443, 8080]
  interval: 10s

otel:
  endpoint: "localhost:4317"
  insecure: true

prometheus:
  enabled: true
  port: 9400
```

## Metrics

The agent exports the following metrics:

- `http.server.duration` - HTTP request duration (histogram)
- `http.server.request_count` - Total HTTP requests (counter)
- `http.server.error_count` - HTTP errors (counter)
- `db.client.duration` - Database call duration (histogram)

## Supported Languages

| Language | HTTP | HTTPS/TLS | Database |
|----------|------|------------|----------|
| Go       | ✓   | ✓         | ✓        |
| Java     | ✓   | Limited    | ✓        |
| Python   | ✓   | ✓         | ✓        |
| Node.js  | ✓   | ✓         | ✓        |
| .NET     | ✓   | Limited   | ✓        |
| Rust     | ✓   | ✓         | ✓        |

## License

Apache License 2.0
