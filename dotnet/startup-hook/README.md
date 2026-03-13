# Lang-Ango: Enterprise APM Solution

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  Go Agent (Command Center)                                        │
│  - OTLP Export to Jaeger                                          │
│  - eBPF Network Tracing                                           │
│  - Config Hot-Reload (fsnotify)                                   │
│  - Adaptive Sampling Logic                                        │
└───────────────────────┬─────────────────────────────────────────┘
                        │ Unix Socket IPC
┌───────────────────────▼─────────────────────────────────────────┐
│  C++ Bridge (liblangango_bridge.so)                              │
│  - Ring Buffer (1024 items, lock-free)                           │
│  - Background flush thread                                        │
│  - Command listener thread                                       │
└───────────────────────┬─────────────────────────────────────────┘
                        │ P/Invoke
┌───────────────────────▼─────────────────────────────────────────┐
│  .NET StartupHook (DiagnosticSource)                             │
│  - Microsoft.AspNetCore                                           │
│  - System.Net.Http                                                │
│  - Microsoft.EntityFrameworkCore                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Quick Start

### 1. Start Go Agent
```bash
cd /home/roy/github-projects/lang-ango
./bin/lang-ango -config config.yaml
```

### 2. Build C++ Bridge
```bash
cd dotnet/startup-hook
make
```

### 3. Run .NET App with StartupHook
```bash
export DOTNET_STARTUP_HOOKS=/path/to/LangAngo.StartupHook.dll
export LD_LIBRARY_PATH=/path/to/startup-hook:$LD_LIBRARY_PATH
dotnet run
```

### 4. View Traces
Open Jaeger: http://localhost:16686

## Files

- `dotnet/startup-hook/bridge.cpp` - C++ Bridge with Ring Buffer
- `dotnet/startup-hook/liblangango_bridge.so` - Compiled bridge library
- `dotnet/startup-hook/LangAngo.StartupHook.cs` - .NET StartupHook
- `pkg/agent/hybrid/agent.go` - Go IPC Server

## IPC Protocol

Message format (19 byte header + payload):
- Magic: 0x4C414E47 ("LANG")
- Version: 1
- Type: 1=Span, 3=ThreadSample, 4=Exception, 5=Heartbeat
- PayloadSize: bytes
- Sequence: counter

## Adaptive Sampling

Go Agent sends commands to Bridge:
- CMD_SET_FILTER: Set endpoint filter and slow threshold
- CMD_START_STACK: Enable stack capture
- CMD_STOP_STACK: Disable stack capture
