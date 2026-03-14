#!/bin/bash
# Lang-Ango End-to-End Test Setup
# This script starts the entire system for load testing

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

echo "=========================================="
echo "Lang-Ango E2E Test Setup"
echo "=========================================="

# 1. Build Go agent
echo "[1/6] Building Go agent..."
make build

# 2. Build C++ bridge
echo "[2/6] Building C++ Bridge..."
cd dotnet/startup-hook
make
cd "$PROJECT_ROOT"

# 3. Start Go agent in background
echo "[3/6] Starting Go agent..."
rm -f /tmp/langango.sock
./bin/lang-ango -config config.yaml --test-mode > /tmp/lang-ango.log 2>&1 &
AGENT_PID=$!
sleep 2

# Verify agent is running
if ! kill -0 $AGENT_PID 2>/dev/null; then
    echo "ERROR: Agent failed to start"
    cat /tmp/lang-ango.log
    exit 1
fi
echo "Agent started (PID: $AGENT_PID)"

# 4. Build .NET Sample API
echo "[4/6] Building Sample API..."
cd test/load/SampleApi
dotnet build -c Release

# 5. Start Sample API with StartupHook
echo "[5/6] Starting Sample API with StartupHook..."
export DOTNET_STARTUP_HOOKS="$PROJECT_ROOT/dotnet/startup-hook/LangAngo.StartupHook/bin/Release/net9.0/langango.dll"
export LD_LIBRARY_PATH="$PROJECT_ROOT/dotnet/startup-hook:$LD_LIBRARY_PATH"
export LANGANGO_SERVICE_NAME=SampleApi

dotnet run --urls "http://localhost:5000" --no-build > /tmp/sample-api.log 2>&1 &
API_PID=$!
sleep 3

if ! kill -0 $API_PID 2>/dev/null; then
    echo "ERROR: Sample API failed to start"
    cat /tmp/sample-api.log
    exit 1
fi
echo "Sample API started (PID: $API_PID)"

# 6. Verify system is ready
echo "[6/6] Verifying system..."
sleep 1

if curl -s http://localhost:5000/api/data > /dev/null; then
    echo "Sample API responding!"
else
    echo "WARNING: Sample API not responding yet"
fi

# Check Jaeger
if curl -s http://localhost:16686 > /dev/null; then
    echo "Jaeger UI available at http://localhost:16686"
else
    echo "WARNING: Jaeger not running. Start with: docker-compose up -d jaeger"
fi

echo ""
echo "=========================================="
echo "System Ready!"
echo "=========================================="
echo "Agent PID: $AGENT_PID"
echo "API PID: $API_PID"
echo ""
echo "Test endpoints:"
echo "  - API: http://localhost:5000/api/data"
echo "  - Jaeger: http://localhost:16686"
echo ""
echo "Run load test:"
echo "  cd test/load && k6 run load-test.js"
echo ""
echo "Stop:"
echo "  kill $AGENT_PID $API_PID"
echo ""

# Wait for interrupt
trap "echo 'Shutting down...'; kill $AGENT_PID $API_PID 2>/dev/null; exit 0" INT TERM

wait
