#!/bin/bash
# Upload source to one seed node, then fan out to all others in parallel.
# ~3x faster than sequential: one slow upload + fast parallel inter-node transfers.
#
# Usage:
#   ./scripts/upload-source-fanout.sh --env devnet
#   ./scripts/upload-source-fanout.sh --env testnet

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCHIVE="/tmp/network-source.tar.gz"
CONF="$SCRIPT_DIR/remote-nodes.conf"
REMOTE_ARCHIVE="/tmp/network-source.tar.gz"

if [ ! -f "$ARCHIVE" ]; then
    echo "Error: $ARCHIVE not found"
    echo "Run: make build-linux && ./scripts/generate-source-archive.sh"
    exit 1
fi

if [ "$1" != "--env" ] || [ -z "$2" ]; then
    echo "Usage: $0 --env <devnet|testnet>"
    exit 1
fi

ENV="$2"

# Parse all nodes for this environment
declare -a USERS HOSTS PASSES KEYS
i=0
while IFS='|' read -r env userhost pass role key; do
    [ -z "$env" ] && continue
    case "$env" in \#*) continue;; esac
    env="$(echo "$env" | xargs)"
    [ "$env" != "$ENV" ] && continue

    USERS[$i]="${userhost%%@*}"
    HOSTS[$i]="${userhost##*@}"
    PASSES[$i]="$pass"
    KEYS[$i]="$(echo "${key:-}" | xargs)"
    ((i++))
done < "$CONF"

TOTAL=${#HOSTS[@]}
if [ "$TOTAL" -eq 0 ]; then
    echo "No nodes found for environment: $ENV"
    exit 1
fi

echo "Source archive: $ARCHIVE ($(du -h "$ARCHIVE" | cut -f1))"
echo "Fanout: upload to 1 seed, then parallel to $((TOTAL - 1)) others"
echo ""

# --- Helper functions ---

run_ssh() {
    local user="$1" host="$2" pass="$3" key="$4"
    shift 4
    local opts="-o StrictHostKeyChecking=no -o ConnectTimeout=10"
    if [ -n "$key" ]; then
        ssh -n $opts -i "$key" "$user@$host" "$@"
    elif [ -n "$pass" ]; then
        sshpass -p "$pass" ssh -n $opts \
            -o PreferredAuthentications=password -o PubkeyAuthentication=no \
            "$user@$host" "$@"
    else
        ssh -n $opts "$user@$host" "$@"
    fi
}

# Like run_ssh but without -n, so stdin can be piped through
run_ssh_stdin() {
    local user="$1" host="$2" pass="$3" key="$4"
    shift 4
    local opts="-o StrictHostKeyChecking=no -o ConnectTimeout=10"
    if [ -n "$key" ]; then
        ssh $opts -i "$key" "$user@$host" "$@"
    elif [ -n "$pass" ]; then
        sshpass -p "$pass" ssh $opts \
            -o PreferredAuthentications=password -o PubkeyAuthentication=no \
            "$user@$host" "$@"
    else
        ssh $opts "$user@$host" "$@"
    fi
}

run_scp() {
    local user="$1" host="$2" pass="$3" key="$4" src="$5" dst="$6"
    local opts="-o StrictHostKeyChecking=no -o ConnectTimeout=10"
    if [ -n "$key" ]; then
        scp $opts -i "$key" "$src" "$user@$host:$dst"
    elif [ -n "$pass" ]; then
        sshpass -p "$pass" scp $opts \
            -o PreferredAuthentications=password -o PubkeyAuthentication=no \
            "$src" "$user@$host:$dst"
    else
        scp $opts "$src" "$user@$host:$dst"
    fi
}

extract_on_node() {
    local user="$1" host="$2" pass="$3" key="$4"
    local sudo_prefix=""
    [ "$user" != "root" ] && sudo_prefix="sudo "
    run_ssh "$user" "$host" "$pass" "$key" \
        "${sudo_prefix}bash -c 'rm -rf /opt/orama/src && mkdir -p /opt/orama/src /opt/orama/bin && tar xzf $REMOTE_ARCHIVE -C /opt/orama/src 2>/dev/null && if [ -f /opt/orama/src/bin-linux/orama ]; then cp /opt/orama/src/bin-linux/orama /usr/local/bin/orama && chmod +x /usr/local/bin/orama; fi && echo \"\$(ls /opt/orama/src/ | wc -l) files\"'"
}

# --- Step 1: Upload to seed (first node) ---

SEED_USER="${USERS[0]}"
SEED_HOST="${HOSTS[0]}"
SEED_PASS="${PASSES[0]}"
SEED_KEY="${KEYS[0]}"

echo "=== Step 1/3: Upload to seed ($SEED_USER@$SEED_HOST) ==="
run_scp "$SEED_USER" "$SEED_HOST" "$SEED_PASS" "$SEED_KEY" "$ARCHIVE" "$REMOTE_ARCHIVE"
extract_on_node "$SEED_USER" "$SEED_HOST" "$SEED_PASS" "$SEED_KEY"
echo "  ✓ Seed ready"
echo ""

# --- Step 2: Install sshpass on seed if needed ---

echo "=== Step 2/3: Prepare seed for fanout ==="
run_ssh "$SEED_USER" "$SEED_HOST" "$SEED_PASS" "$SEED_KEY" \
    "which sshpass >/dev/null 2>&1 || (sudo apt-get update -qq >/dev/null 2>&1 && sudo apt-get install -y -qq sshpass >/dev/null 2>&1)"
echo "  ✓ sshpass available on seed"
echo ""

# --- Step 3: Fan out from seed to all other nodes in parallel ---

echo "=== Step 3/3: Fanout to $((TOTAL - 1)) nodes ==="

# Collect nodes that need key-based auth (can't fanout, key is local)
declare -a KEY_NODES

# Build a targets file for the seed: user|host|pass|is_root (one per line, base64-encoded passwords)
TARGETS_CONTENT=""
for ((j=1; j<TOTAL; j++)); do
    if [ -n "${KEYS[$j]}" ]; then
        KEY_NODES+=("$j")
        continue
    fi
    # Base64-encode password to avoid shell escaping issues
    b64pass=$(echo -n "${PASSES[$j]}" | base64)
    is_root="0"
    [ "${USERS[$j]}" = "root" ] && is_root="1"
    TARGETS_CONTENT+="${USERS[$j]}|${HOSTS[$j]}|${b64pass}|${is_root}"$'\n'
done

# Upload targets file and fanout script to seed
run_ssh_stdin "$SEED_USER" "$SEED_HOST" "$SEED_PASS" "$SEED_KEY" "cat > /tmp/fanout-targets.txt" <<< "$TARGETS_CONTENT"

FANOUT='#!/bin/bash
ARCHIVE="/tmp/network-source.tar.gz"
PIDS=()
LABELS=()

while IFS="|" read -r user host b64pass is_root; do
    [ -z "$user" ] && continue
    pass=$(echo "$b64pass" | base64 -d)
    sudo_prefix=""
    [ "$is_root" != "1" ] && sudo_prefix="sudo "

    (
        sshpass -p "$pass" scp \
            -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
            -o PreferredAuthentications=password -o PubkeyAuthentication=no \
            "$ARCHIVE" "$user@$host:$ARCHIVE" && \
        sshpass -p "$pass" ssh -n \
            -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
            -o PreferredAuthentications=password -o PubkeyAuthentication=no \
            "$user@$host" \
            "${sudo_prefix}bash -c '\''rm -rf /opt/orama/src && mkdir -p /opt/orama/src /opt/orama/bin && tar xzf /tmp/network-source.tar.gz -C /opt/orama/src 2>/dev/null && if [ -f /opt/orama/src/bin-linux/orama ]; then cp /opt/orama/src/bin-linux/orama /usr/local/bin/orama && chmod +x /usr/local/bin/orama; fi'\''" && \
        echo "  ✓ $user@$host" || \
        echo "  ✗ $user@$host FAILED"
    ) &
    PIDS+=($!)
    LABELS+=("$user@$host")
done < /tmp/fanout-targets.txt

FAILED=0
for i in "${!PIDS[@]}"; do
    if ! wait "${PIDS[$i]}"; then
        FAILED=1
    fi
done

rm -f /tmp/fanout-targets.txt /tmp/fanout.sh
exit $FAILED
'

run_ssh_stdin "$SEED_USER" "$SEED_HOST" "$SEED_PASS" "$SEED_KEY" "cat > /tmp/fanout.sh && chmod +x /tmp/fanout.sh" <<< "$FANOUT"

# Run fanout (allocate tty for live output)
run_ssh "$SEED_USER" "$SEED_HOST" "$SEED_PASS" "$SEED_KEY" "bash /tmp/fanout.sh"

# Handle key-based auth nodes directly from local (key isn't on seed)
for idx in "${KEY_NODES[@]}"; do
    echo ""
    echo "→ Direct upload to ${USERS[$idx]}@${HOSTS[$idx]} (SSH key auth)..."
    run_scp "${USERS[$idx]}" "${HOSTS[$idx]}" "${PASSES[$idx]}" "${KEYS[$idx]}" "$ARCHIVE" "$REMOTE_ARCHIVE"
    extract_on_node "${USERS[$idx]}" "${HOSTS[$idx]}" "${PASSES[$idx]}" "${KEYS[$idx]}"
    echo "  ✓ ${USERS[$idx]}@${HOSTS[$idx]}"
done

echo ""
echo "Done. All $TOTAL nodes updated."
echo "Now run: ./bin/orama install --vps-ip <ip> ..."
