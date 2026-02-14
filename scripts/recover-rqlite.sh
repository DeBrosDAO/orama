#!/usr/bin/env bash
#
# Recover RQLite cluster from split-brain.
#
# Strategy:
#   1. Stop debros-node on ALL nodes simultaneously
#   2. Keep raft/ data ONLY on the node with the highest commit index (leader candidate)
#   3. Delete raft/ on all other nodes (they'll join fresh via -join)
#   4. Start the leader candidate first, wait for it to become Leader
#   5. Start all other nodes — they discover the leader via LibP2P and join
#   6. Verify cluster health
#
# Usage:
#   scripts/recover-rqlite.sh --devnet --leader 57.129.7.232
#   scripts/recover-rqlite.sh --testnet --leader <ip>
#
set -euo pipefail

# ── Parse flags ──────────────────────────────────────────────────────────────
ENV=""
LEADER_HOST=""

for arg in "$@"; do
  case "$arg" in
    --devnet)   ENV="devnet" ;;
    --testnet)  ENV="testnet" ;;
    --leader=*) LEADER_HOST="${arg#--leader=}" ;;
    -h|--help)
      echo "Usage: scripts/recover-rqlite.sh --devnet|--testnet --leader=<public_ip_or_user@host>"
      exit 0
      ;;
    *)
      echo "Unknown flag: $arg" >&2
      exit 1
      ;;
  esac
done

if [[ -z "$ENV" ]]; then
  echo "ERROR: specify --devnet or --testnet" >&2
  exit 1
fi
if [[ -z "$LEADER_HOST" ]]; then
  echo "ERROR: specify --leader=<host> (the node with highest commit index)" >&2
  exit 1
fi

# ── Paths ────────────────────────────────────────────────────────────────────
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF="$ROOT_DIR/scripts/remote-nodes.conf"

die() { echo "ERROR: $*" >&2; exit 1; }
[[ -f "$CONF" ]] || die "Missing $CONF"

# ── Load nodes from conf ────────────────────────────────────────────────────
HOSTS=()
PASSES=()
ROLES=()
SSH_KEYS=()

while IFS='|' read -r env host pass role key; do
  [[ -z "$env" || "$env" == \#* ]] && continue
  env="${env%%#*}"
  env="$(echo "$env" | xargs)"
  [[ "$env" != "$ENV" ]] && continue

  HOSTS+=("$host")
  PASSES+=("$pass")
  ROLES+=("${role:-node}")
  SSH_KEYS+=("${key:-}")
done < "$CONF"

if [[ ${#HOSTS[@]} -eq 0 ]]; then
  die "No nodes found for environment '$ENV' in $CONF"
fi

echo "== recover-rqlite.sh ($ENV) — ${#HOSTS[@]} nodes =="
echo "Leader candidate: $LEADER_HOST"
echo ""

# Find leader index
LEADER_IDX=-1
for i in "${!HOSTS[@]}"; do
  if [[ "${HOSTS[$i]}" == *"$LEADER_HOST"* ]]; then
    LEADER_IDX=$i
    break
  fi
done

if [[ $LEADER_IDX -eq -1 ]]; then
  die "Leader host '$LEADER_HOST' not found in node list"
fi

echo "Nodes:"
for i in "${!HOSTS[@]}"; do
  marker=""
  [[ $i -eq $LEADER_IDX ]] && marker=" ← LEADER (keep data)"
  echo "  [$i] ${HOSTS[$i]} (${ROLES[$i]})$marker"
done
echo ""

# ── SSH helpers ──────────────────────────────────────────────────────────────
SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10)

node_ssh() {
  local idx="$1"
  shift
  local h="${HOSTS[$idx]}"
  local p="${PASSES[$idx]}"
  local k="${SSH_KEYS[$idx]:-}"

  if [[ -n "$k" ]]; then
    local expanded_key="${k/#\~/$HOME}"
    if [[ -f "$expanded_key" ]]; then
      ssh -i "$expanded_key" "${SSH_OPTS[@]}" "$h" "$@" 2>/dev/null
      return $?
    fi
  fi
  sshpass -p "$p" ssh -n "${SSH_OPTS[@]}" "$h" "$@" 2>/dev/null
}

# ── Confirmation ─────────────────────────────────────────────────────────────
echo "⚠️  THIS WILL:"
echo "  1. Stop debros-node on ALL ${#HOSTS[@]} nodes"
echo "  2. DELETE raft/ data on ${#HOSTS[@]}-1 nodes (backup to /tmp/rqlite-raft-backup/)"
echo "  3. Keep raft/ data ONLY on ${HOSTS[$LEADER_IDX]} (leader candidate)"
echo "  4. Restart all nodes to reform the cluster"
echo ""
read -r -p "Continue? [y/N] " confirm
if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
  echo "Aborted."
  exit 0
fi
echo ""

RAFT_DIR="/home/debros/.orama/data/rqlite/raft"
BACKUP_DIR="/tmp/rqlite-raft-backup"

# ── Phase 1: Stop debros-node on ALL nodes ───────────────────────────────────
echo "== Phase 1: Stopping debros-node on all ${#HOSTS[@]} nodes =="
failed=()
for i in "${!HOSTS[@]}"; do
  h="${HOSTS[$i]}"
  p="${PASSES[$i]}"
  echo -n "  Stopping $h ... "
  if node_ssh "$i" "printf '%s\n' '$p' | sudo -S systemctl stop debros-node 2>&1 && echo STOPPED"; then
    echo ""
  else
    echo "FAILED"
    failed+=("$h")
  fi
done

if [[ ${#failed[@]} -gt 0 ]]; then
  echo ""
  echo "⚠️  ${#failed[@]} nodes failed to stop. Attempting kill..."
  for i in "${!HOSTS[@]}"; do
    h="${HOSTS[$i]}"
    p="${PASSES[$i]}"
    for fh in "${failed[@]}"; do
      if [[ "$h" == "$fh" ]]; then
        node_ssh "$i" "printf '%s\n' '$p' | sudo -S killall -9 orama-node rqlited 2>/dev/null; echo KILLED" || true
      fi
    done
  done
fi

echo ""
echo "Waiting 5s for processes to fully stop..."
sleep 5

# ── Phase 2: Backup and delete raft/ on non-leader nodes ────────────────────
echo "== Phase 2: Clearing raft state on non-leader nodes =="
for i in "${!HOSTS[@]}"; do
  [[ $i -eq $LEADER_IDX ]] && continue

  h="${HOSTS[$i]}"
  p="${PASSES[$i]}"
  echo -n "  Clearing $h ... "
  if node_ssh "$i" "
    printf '%s\n' '$p' | sudo -S bash -c '
      rm -rf $BACKUP_DIR
      if [ -d $RAFT_DIR ]; then
        cp -r $RAFT_DIR $BACKUP_DIR 2>/dev/null || true
        rm -rf $RAFT_DIR
        echo \"CLEARED (backup at $BACKUP_DIR)\"
      else
        echo \"NO_RAFT_DIR (nothing to clear)\"
      fi
    '
  "; then
    true
  else
    echo "FAILED"
  fi
done

echo ""
echo "Leader node ${HOSTS[$LEADER_IDX]} raft/ data preserved."

# ── Phase 3: Start leader node ──────────────────────────────────────────────
echo ""
echo "== Phase 3: Starting leader node (${HOSTS[$LEADER_IDX]}) =="
lp="${PASSES[$LEADER_IDX]}"
node_ssh "$LEADER_IDX" "printf '%s\n' '$lp' | sudo -S systemctl start debros-node" || die "Failed to start leader node"

echo "  Waiting for leader to become Leader..."
max_wait=120
elapsed=0
while [[ $elapsed -lt $max_wait ]]; do
  state=$(node_ssh "$LEADER_IDX" "curl -s --max-time 3 http://localhost:5001/status 2>/dev/null | python3 -c \"import sys,json; d=json.load(sys.stdin); print(d.get('store',{}).get('raft',{}).get('state',''))\" 2>/dev/null" || echo "")
  if [[ "$state" == "Leader" ]]; then
    echo "  ✓ Leader node is Leader after ${elapsed}s"
    break
  fi
  echo "  ... state=$state (${elapsed}s / ${max_wait}s)"
  sleep 5
  ((elapsed+=5))
done

if [[ "$state" != "Leader" ]]; then
  echo "  ⚠️  Leader did not become Leader within ${max_wait}s (state=$state)"
  echo "  The node may need more time. Continuing anyway..."
fi

# ── Phase 4: Start all other nodes ──────────────────────────────────────────
echo ""
echo "== Phase 4: Starting remaining nodes =="

# Start non-leader nodes in batches of 3 with 15s between batches
batch_size=3
batch_count=0
for i in "${!HOSTS[@]}"; do
  [[ $i -eq $LEADER_IDX ]] && continue

  h="${HOSTS[$i]}"
  p="${PASSES[$i]}"
  echo -n "  Starting $h ... "
  if node_ssh "$i" "printf '%s\n' '$p' | sudo -S systemctl start debros-node && echo STARTED"; then
    true
  else
    echo "FAILED"
  fi

  ((batch_count++))
  if [[ $((batch_count % batch_size)) -eq 0 ]]; then
    echo "  (waiting 15s between batches for cluster stability)"
    sleep 15
  fi
done

# ── Phase 5: Wait and verify ────────────────────────────────────────────────
echo ""
echo "== Phase 5: Waiting for cluster to form (120s) =="
sleep 30
echo "  ... 30s"
sleep 30
echo "  ... 60s"
sleep 30
echo "  ... 90s"
sleep 30
echo "  ... 120s"

echo ""
echo "== Cluster status =="
for i in "${!HOSTS[@]}"; do
  h="${HOSTS[$i]}"
  result=$(node_ssh "$i" "curl -s --max-time 5 http://localhost:5001/status 2>/dev/null | python3 -c \"
import sys,json
try:
  d=json.load(sys.stdin)
  r=d.get('store',{}).get('raft',{})
  n=d.get('store',{}).get('num_nodes','?')
  print(f'state={r.get(\"state\",\"?\")} commit={r.get(\"commit_index\",\"?\")} leader={r.get(\"leader\",{}).get(\"node_id\",\"?\")} nodes={n}')
except:
  print('NO_RESPONSE')
\" 2>/dev/null" || echo "SSH_FAILED")
  marker=""
  [[ $i -eq $LEADER_IDX ]] && marker=" ← LEADER"
  echo "  ${HOSTS[$i]}: $result$marker"
done

echo ""
echo "== Recovery complete =="
echo ""
echo "Next steps:"
echo "  1. Run 'scripts/inspect.sh --devnet' to verify full cluster health"
echo "  2. If some nodes show Candidate state, give them more time (up to 5 min)"
echo "  3. If nodes fail to join, check /home/debros/.orama/logs/rqlite-node.log on the node"
