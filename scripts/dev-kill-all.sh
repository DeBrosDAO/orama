#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DBN="$ROOT_DIR/bin/dbn"

# First, try graceful shutdown via dbn dev down
if [[ -x "$BIN_DBN" ]]; then
  echo "Attempting graceful shutdown via dbn dev down..."
  "$BIN_DBN" dev down || true
  sleep 1
fi

# Clean up PID files
PIDS_DIR="$HOME/.debros/.pids"
if [[ -d "$PIDS_DIR" ]]; then
  echo "Removing stale PID files..."
  rm -f "$PIDS_DIR"/*.pid || true
fi

# Force kill any lingering processes
echo "Force killing any remaining dev processes..."

declare -a PATTERNS=(
  "ipfs daemon"
  "ipfs-cluster-service daemon"
  "rqlited"
  "olric-server"
  "anyone-client"
  "bin/node"
  "bin/gateway"
)

killed_count=0
for pattern in "${PATTERNS[@]}"; do
  count=$(pgrep -f "$pattern" | wc -l)
  if [[ $count -gt 0 ]]; then
    pkill -f "$pattern" || true
    echo "✓ Killed $count process(es) matching: $pattern"
    killed_count=$((killed_count + count))
  fi
done

if [[ $killed_count -eq 0 ]]; then
  echo "✓ No lingering dev processes found"
else
  echo "✓ Terminated $killed_count process(es) total"
  sleep 1
fi

# Verify ports are free
echo ""
echo "Verifying ports are now available..."
PORTS=(4001 4501 4502 4503 5001 5002 5003 6001 7001 7002 7003 9094 9104 9114 3320 3322 9050)
unavailable=()

for port in "${PORTS[@]}"; do
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    unavailable+=("$port")
  fi
done

if [[ ${#unavailable[@]} -eq 0 ]]; then
  echo "✓ All required ports are free"
else
  echo "⚠️  WARNING: These ports are still in use: ${unavailable[*]}"
  echo "Use 'lsof -nP -i :PORT' to identify the processes"
fi

