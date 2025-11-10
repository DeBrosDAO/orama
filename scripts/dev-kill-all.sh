#!/bin/bash
set -euo pipefail

echo "Force killing all processes on dev ports..."

# Define all dev ports
PORTS=(4001 4002 4003 4101 4102 4103 4501 4502 4503 5001 5002 5003 6001 7001 7002 7003 7501 7502 7503 8080 8081 8082 9094 9095 9096 9097 9104 9105 9106 9107 9114 9115 9116 9117 3320 3322 9050)

killed_count=0
killed_pids=()

# Kill all processes using these ports (LISTEN, ESTABLISHED, or any state)
for port in "${PORTS[@]}"; do
  # Get all PIDs using this port in ANY TCP state
  pids=$(lsof -nP -iTCP:"$port" -t 2>/dev/null || true)
  if [[ -n "$pids" ]]; then
    echo "Killing processes on port $port: $pids"
    for pid in $pids; do
      # Kill the process and all its children
      kill -9 "$pid" 2>/dev/null || true
      # Also kill any children of this process
      pkill -9 -P "$pid" 2>/dev/null || true
      killed_pids+=("$pid")
    done
    killed_count=$((killed_count + 1))
  fi
done

# Also kill processes by command name patterns (in case they're orphaned)
# This catches processes that might be using debros ports but not showing up in lsof
COMMANDS=("node" "ipfs" "ipfs-cluster-service" "rqlited" "olric-server" "gateway")
for cmd in "${COMMANDS[@]}"; do
  # Find all processes with this command name
  all_pids=$(pgrep -f "^.*$cmd.*" 2>/dev/null || true)
  if [[ -n "$all_pids" ]]; then
    for pid in $all_pids; do
      # Check if this process is using any of our dev ports
      port_match=$(lsof -nP -p "$pid" -iTCP 2>/dev/null | grep -E ":(400[1-3]|410[1-3]|450[1-3]|500[1-3]|6001|700[1-3]|750[1-3]|808[0-2]|909[4-7]|910[4-7]|911[4-7]|332[02]|9050)" || true)
      if [[ -n "$port_match" ]]; then
        echo "Killing orphaned $cmd process (PID: $pid) using dev ports"
        kill -9 "$pid" 2>/dev/null || true
        pkill -9 -P "$pid" 2>/dev/null || true
        killed_pids+=("$pid")
      fi
    done
  fi
done

# Clean up PID files
PIDS_DIR="$HOME/.debros/.pids"
if [[ -d "$PIDS_DIR" ]]; then
  rm -f "$PIDS_DIR"/*.pid || true
fi

# Remove duplicates and report
if [[ ${#killed_pids[@]} -gt 0 ]]; then
  unique_pids=($(printf '%s\n' "${killed_pids[@]}" | sort -u))
  echo "✓ Killed ${#unique_pids[@]} unique process(es) on $killed_count port(s)"
else
  echo "✓ No processes found on dev ports"
fi

# Final verification: check if any ports are still in use
still_in_use=0
for port in "${PORTS[@]}"; do
  pids=$(lsof -nP -iTCP:"$port" -t 2>/dev/null || true)
  if [[ -n "$pids" ]]; then
    echo "⚠️  Warning: Port $port still in use by: $pids"
    still_in_use=$((still_in_use + 1))
  fi
done

if [[ $still_in_use -eq 0 ]]; then
  echo "✓ All dev ports are now free"
else
  echo "⚠️  $still_in_use port(s) still in use - you may need to manually kill processes"
fi

