#!/bin/bash
set -euo pipefail

echo "Force killing all processes on dev ports..."

# Define all dev ports
PORTS=(4001 4501 4502 4503 5001 5002 5003 6001 7001 7002 7003 9094 9104 9114 3320 3322 9050 8080 8081 8082)

killed_count=0
for port in "${PORTS[@]}"; do
  # Get all PIDs listening on this port
  pids=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null || true)
  if [[ -n "$pids" ]]; then
    echo "Killing processes on port $port: $pids"
    echo "$pids" | xargs -r kill -9 || true
    killed_count=$((killed_count + 1))
  fi
done

# Clean up PID files
PIDS_DIR="$HOME/.debros/.pids"
if [[ -d "$PIDS_DIR" ]]; then
  rm -f "$PIDS_DIR"/*.pid || true
fi

if [[ $killed_count -eq 0 ]]; then
  echo "✓ No processes found on dev ports"
else
  echo "✓ Killed processes on $killed_count port(s)"
fi

echo "✓ All dev ports should now be free"

