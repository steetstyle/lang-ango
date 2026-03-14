#!/bin/bash
# Simple load test using curl

echo "Starting simple load test..."
echo "Press Ctrl+C to stop"
echo ""

COUNT=0
while true; do
    # Make request
    curl -s -w "\nTime: %{time_total}s\n" http://localhost:5000/api/data 2>/dev/null &
    
    COUNT=$((COUNT + 1))
    if [ $((COUNT % 10)) -eq 0 ]; then
        echo "Requests sent: $COUNT"
    fi
    
    # Small delay to control rate
    sleep 0.1
done
