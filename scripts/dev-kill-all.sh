#!/bin/bash
set -euo pipefail

echo "Force killing all debros development processes..."

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

# Add namespace cluster ports (10000-10099)
# These are dynamically allocated for per-namespace RQLite/Olric/Gateway instances
for port in $(seq 10000 10099); do
  PORTS+=($port)
done

killed_count=0
killed_pids=()

# Method 1: Kill all processes using these ports
for port in "${PORTS[@]}"; do
  pids=$(lsof -nP -iTCP:"$port" -t 2>/dev/null || true)
  if [[ -n "$pids" ]]; then
    echo "  Killing processes on port $port: $pids"
    for pid in $pids; do
      kill -9 "$pid" 2>/dev/null || true
      pkill -9 -P "$pid" 2>/dev/null || true
      killed_pids+=("$pid")
    done
    killed_count=$((killed_count + 1))
  fi
done

# Method 2: Kill processes by specific patterns (ONLY debros-related)
# Be very specific to avoid killing unrelated processes
SPECIFIC_PATTERNS=(
  "ipfs daemon"
  "ipfs-cluster-service daemon"
  "olric-server"
  "bin/orama-node"
  "bin/gateway"
  "anyone-client"
)

# Kill namespace cluster processes (spawned by ClusterManager)
# These are RQLite/Olric/Gateway instances running on ports 10000-10099
NAMESPACE_DATA_DIR="$HOME/.orama/data/namespaces"
if [[ -d "$NAMESPACE_DATA_DIR" ]]; then
  # Find rqlited processes started in namespace directories
  ns_pids=$(pgrep -f "rqlited.*$NAMESPACE_DATA_DIR" 2>/dev/null || true)
  if [[ -n "$ns_pids" ]]; then
    for pid in $ns_pids; do
      echo "  Killing namespace rqlited process (PID: $pid)"
      kill -9 "$pid" 2>/dev/null || true
      killed_pids+=("$pid")
    done
  fi

  # Find olric-server processes started for namespaces (check env var or config path)
  ns_olric_pids=$(pgrep -f "olric-server.*$NAMESPACE_DATA_DIR" 2>/dev/null || true)
  if [[ -n "$ns_olric_pids" ]]; then
    for pid in $ns_olric_pids; do
      echo "  Killing namespace olric-server process (PID: $pid)"
      kill -9 "$pid" 2>/dev/null || true
      killed_pids+=("$pid")
    done
  fi

  # Find gateway processes started for namespaces
  ns_gw_pids=$(pgrep -f "gateway.*--config.*$NAMESPACE_DATA_DIR" 2>/dev/null || true)
  if [[ -n "$ns_gw_pids" ]]; then
    for pid in $ns_gw_pids; do
      echo "  Killing namespace gateway process (PID: $pid)"
      kill -9 "$pid" 2>/dev/null || true
      killed_pids+=("$pid")
    done
  fi
fi

for pattern in "${SPECIFIC_PATTERNS[@]}"; do
  # Use exact pattern matching to avoid false positives
  all_pids=$(pgrep -f "$pattern" 2>/dev/null || true)
  if [[ -n "$all_pids" ]]; then
    for pid in $all_pids; do
      # Double-check the command line to avoid killing wrong processes
      cmdline=$(ps -p "$pid" -o command= 2>/dev/null || true)
      if [[ "$cmdline" == *"$pattern"* ]]; then
        echo "  Killing $pattern process (PID: $pid)"
        kill -9 "$pid" 2>/dev/null || true
        pkill -9 -P "$pid" 2>/dev/null || true
        killed_pids+=("$pid")
      fi
    done
  fi
done

# Method 3: Kill processes using PID files
PIDS_DIR="$HOME/.orama/.pids"
if [[ -d "$PIDS_DIR" ]]; then
  for pidfile in "$PIDS_DIR"/*.pid; do
    if [[ -f "$pidfile" ]]; then
      pid=$(cat "$pidfile" 2>/dev/null || true)
      if [[ -n "$pid" ]] && ps -p "$pid" > /dev/null 2>&1; then
        name=$(basename "$pidfile" .pid)
        echo "  Killing $name (PID: $pid from pidfile)"
        kill -9 "$pid" 2>/dev/null || true
        pkill -9 -P "$pid" 2>/dev/null || true
        killed_pids+=("$pid")
      fi
    fi
  done
  # Clean up all PID files
  rm -f "$PIDS_DIR"/*.pid 2>/dev/null || true
fi

# Remove duplicates and report
if [[ ${#killed_pids[@]} -gt 0 ]]; then
  unique_pids=($(printf '%s\n' "${killed_pids[@]}" | sort -u))
  echo "✓ Killed ${#unique_pids[@]} unique process(es)"
else
  echo "✓ No debros processes found running"
fi

# Final verification: check if any ports are still in use
still_in_use=0
busy_ports=()
for port in "${PORTS[@]}"; do
  pids=$(lsof -nP -iTCP:"$port" -t 2>/dev/null || true)
  if [[ -n "$pids" ]]; then
    busy_ports+=("$port")
    still_in_use=$((still_in_use + 1))
  fi
done

if [[ $still_in_use -eq 0 ]]; then
  echo "✓ All dev ports are now free"
else
  echo "⚠️  Warning: $still_in_use port(s) still in use: ${busy_ports[*]}"
  echo "    Run 'lsof -nP -iTCP:<port>' to identify the processes"
fi

