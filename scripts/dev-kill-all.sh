#!/bin/bash
set -euo pipefail

echo "Force killing all processes on dev ports..."

# Define all dev ports (5 nodes topology: bootstrap, bootstrap2, node2, node3, node4)
PORTS=(
  # LibP2P
  4001 4011 4002 4003 4004
  # IPFS Swarm
  4101 4111 4102 4103 4104
  # IPFS API
  4501 4511 4502 4503 4504
  # RQLite HTTP
  5001 5011 5002 5003 5004
  # RQLite Raft
  7001 7011 7002 7003 7004
  # IPFS Gateway
  7501 7511 7502 7503 7504
  # Gateway
  6001
  # Olric
  3320 3322
  # Anon SOCKS
  9050
  # IPFS Cluster REST API
  9094 9104 9114 9124 9134
  # IPFS Cluster P2P
  9096 9106 9116 9126 9136
)

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
      port_match=$(lsof -nP -p "$pid" -iTCP 2>/dev/null | grep -E ":(400[1-4]|401[1-1]|410[1-4]|411[1-1]|450[1-4]|451[1-1]|500[1-4]|501[1-1]|600[1-1]|700[1-4]|701[1-1]|750[1-4]|751[1-1]|332[02]|9050|909[4-9]|910[4-9]|911[4-9]|912[4-9]|913[4-9]|909[6-9]|910[6-9]|911[6-9]|912[6-9]|913[6-9])" || true)
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

