#!/usr/bin/env bash
# block-node.sh - Temporarily block network access to a gateway node (local or remote)
# Usage:
#   Local:  ./scripts/block-node.sh <node_number> <duration_seconds>
#   Remote: ./scripts/block-node.sh --remote <remote_node_number> <duration_seconds>
# Example:
#   ./scripts/block-node.sh 1 60          # Block local node-1 (port 6001) for 60 seconds
#   ./scripts/block-node.sh --remote 2 120  # Block remote node-2 for 120 seconds

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Remote node configurations - loaded from config file
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="$SCRIPT_DIR/remote-nodes.conf"

# Function to get remote node config
get_remote_node_config() {
    local node_num="$1"
    local field="$2"  # "user_host" or "password"

    if [ ! -f "$CONFIG_FILE" ]; then
        echo ""
        return 1
    fi

    while IFS='|' read -r num user_host password || [ -n "$num" ]; do
        # Skip comments and empty lines
        [[ "$num" =~ ^#.*$ ]] || [[ -z "$num" ]] && continue
        # Trim whitespace
        num=$(echo "$num" | xargs)
        user_host=$(echo "$user_host" | xargs)
        password=$(echo "$password" | xargs)

        if [ "$num" = "$node_num" ]; then
            if [ "$field" = "user_host" ]; then
                echo "$user_host"
            elif [ "$field" = "password" ]; then
                echo "$password"
            fi
            return 0
        fi
    done < "$CONFIG_FILE"

    echo ""
    return 1
}

# Display usage
usage() {
    echo -e "${RED}Error:${NC} Invalid arguments"
    echo ""
    echo -e "${BLUE}Usage:${NC}"
    echo "  $0 <node_number> <duration_seconds>                    # Local mode"
    echo "  $0 --remote <remote_node_number> <duration_seconds>    # Remote mode"
    echo ""
    echo -e "${GREEN}Local Mode Examples:${NC}"
    echo "  $0 1 60    # Block local node-1 (port 6001) for 60 seconds"
    echo "  $0 2 120   # Block local node-2 (port 6002) for 120 seconds"
    echo ""
    echo -e "${GREEN}Remote Mode Examples:${NC}"
    echo "  $0 --remote 1 60    # Block remote node-1 (51.83.128.181) for 60 seconds"
    echo "  $0 --remote 3 120   # Block remote node-3 (83.171.248.66) for 120 seconds"
    echo ""
    echo -e "${YELLOW}Local Node Mapping:${NC}"
    echo "  Node 1 -> Port 6001"
    echo "  Node 2 -> Port 6002"
    echo "  Node 3 -> Port 6003"
    echo "  Node 4 -> Port 6004"
    echo "  Node 5 -> Port 6005"
    echo ""
    echo -e "${YELLOW}Remote Node Mapping:${NC}"
    echo "  Remote 1 -> ubuntu@51.83.128.181"
    echo "  Remote 2 -> root@194.61.28.7"
    echo "  Remote 3 -> root@83.171.248.66"
    echo "  Remote 4 -> root@62.72.44.87"
    exit 1
}

# Parse arguments
REMOTE_MODE=false
if [ $# -eq 3 ] && [ "$1" == "--remote" ]; then
    REMOTE_MODE=true
    NODE_NUM="$2"
    DURATION="$3"
elif [ $# -eq 2 ]; then
    NODE_NUM="$1"
    DURATION="$2"
else
    usage
fi

# Validate duration
if ! [[ "$DURATION" =~ ^[0-9]+$ ]] || [ "$DURATION" -le 0 ]; then
    echo -e "${RED}Error:${NC} Duration must be a positive integer"
    exit 1
fi

# Calculate port (local nodes use 6001-6005, remote nodes use 80 and 443)
if [ "$REMOTE_MODE" = true ]; then
    # Remote nodes: block standard HTTP/HTTPS ports
    PORTS="80 443"
else
    # Local nodes: block the specific gateway port
    PORT=$((6000 + NODE_NUM))
fi

# Function to block ports on remote server
block_remote_node() {
    local node_num="$1"
    local duration="$2"
    local ports="$3"  # Can be space-separated list like "80 443"

    # Validate remote node number
    if ! [[ "$node_num" =~ ^[1-4]$ ]]; then
        echo -e "${RED}Error:${NC} Remote node number must be between 1 and 4"
        exit 1
    fi

    # Get credentials from config file
    local user_host=$(get_remote_node_config "$node_num" "user_host")
    local password=$(get_remote_node_config "$node_num" "password")

    if [ -z "$user_host" ] || [ -z "$password" ]; then
        echo -e "${RED}Error:${NC} Configuration for remote node $node_num not found in $CONFIG_FILE"
        exit 1
    fi

    local host="${user_host##*@}"

    echo -e "${BLUE}=== Remote Network Blocking Tool ===${NC}"
    echo -e "Remote Node: ${GREEN}$node_num${NC} ($user_host)"
    echo -e "Ports:       ${GREEN}$ports${NC}"
    echo -e "Duration:    ${GREEN}$duration seconds${NC}"
    echo ""

    # Check if sshpass is installed
    if ! command -v sshpass &> /dev/null; then
        echo -e "${RED}Error:${NC} sshpass is not installed. Install it first:"
        echo -e "  ${YELLOW}macOS:${NC} brew install hudochenkov/sshpass/sshpass"
        echo -e "  ${YELLOW}Ubuntu/Debian:${NC} sudo apt-get install sshpass"
        exit 1
    fi

    # SSH options - force password authentication only to avoid "too many auth failures"
    SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o PreferredAuthentications=password -o PubkeyAuthentication=no -o NumberOfPasswordPrompts=1"

    echo -e "${YELLOW}Connecting to remote server...${NC}"

    # Test connection
    if ! sshpass -p "$password" ssh $SSH_OPTS "$user_host" "echo 'Connected successfully' > /dev/null"; then
        echo -e "${RED}Error:${NC} Failed to connect to $user_host"
        exit 1
    fi

    echo -e "${GREEN}✓${NC} Connected to $host"

    # Install iptables rules on remote server
    echo -e "${YELLOW}Installing iptables rules on remote server...${NC}"

    # Build iptables commands for all ports
    BLOCK_CMDS=""
    for port in $ports; do
        BLOCK_CMDS="${BLOCK_CMDS}iptables -I INPUT -p tcp --dport $port -j DROP 2>/dev/null || true; "
        BLOCK_CMDS="${BLOCK_CMDS}iptables -I OUTPUT -p tcp --sport $port -j DROP 2>/dev/null || true; "
    done
    BLOCK_CMDS="${BLOCK_CMDS}echo 'Rules installed'"

    if ! sshpass -p "$password" ssh $SSH_OPTS "$user_host" "$BLOCK_CMDS"; then
        echo -e "${RED}Error:${NC} Failed to install iptables rules"
        exit 1
    fi

    echo -e "${GREEN}✓${NC} Ports $ports are now blocked on $host"
    echo -e "${YELLOW}Waiting $duration seconds...${NC}"
    echo ""

    # Show countdown
    for ((i=duration; i>0; i--)); do
        printf "\r${BLUE}Time remaining: %3d seconds${NC}" "$i"
        sleep 1
    done

    echo ""
    echo ""
    echo -e "${YELLOW}Removing iptables rules from remote server...${NC}"

    # Build iptables removal commands for all ports
    UNBLOCK_CMDS=""
    for port in $ports; do
        UNBLOCK_CMDS="${UNBLOCK_CMDS}iptables -D INPUT -p tcp --dport $port -j DROP 2>/dev/null || true; "
        UNBLOCK_CMDS="${UNBLOCK_CMDS}iptables -D OUTPUT -p tcp --sport $port -j DROP 2>/dev/null || true; "
    done
    UNBLOCK_CMDS="${UNBLOCK_CMDS}echo 'Rules removed'"

    if ! sshpass -p "$password" ssh $SSH_OPTS "$user_host" "$UNBLOCK_CMDS"; then
        echo -e "${YELLOW}Warning:${NC} Failed to remove some iptables rules. You may need to clean up manually."
    else
        echo -e "${GREEN}✓${NC} Ports $ports are now accessible again on $host"
    fi

    echo ""
    echo -e "${GREEN}=== Done! ===${NC}"
    echo -e "Remote node ${GREEN}$node_num${NC} ($host) was unreachable for $duration seconds and is now accessible again."
}

# Function to block port locally using process pause (SIGSTOP)
block_local_node() {
    local node_num="$1"
    local duration="$2"
    local port="$3"

    # Validate node number
    if ! [[ "$node_num" =~ ^[1-5]$ ]]; then
        echo -e "${RED}Error:${NC} Local node number must be between 1 and 5"
        exit 1
    fi

    echo -e "${BLUE}=== Local Network Blocking Tool ===${NC}"
    echo -e "Node:     ${GREEN}node-$node_num${NC}"
    echo -e "Port:     ${GREEN}$port${NC}"
    echo -e "Duration: ${GREEN}$duration seconds${NC}"
    echo -e "Method:   ${GREEN}Process Pause (SIGSTOP/SIGCONT)${NC}"
    echo ""

    # Find the process listening on the port
    echo -e "${YELLOW}Finding process listening on port $port...${NC}"

    # macOS uses different tools than Linux
    if [[ "$(uname -s)" == "Darwin" ]]; then
        # macOS: use lsof
        PID=$(lsof -ti :$port 2>/dev/null | head -1 || echo "")
    else
        # Linux: use ss or netstat
        if command -v ss &> /dev/null; then
            PID=$(ss -tlnp | grep ":$port " | grep -oP 'pid=\K[0-9]+' | head -1 || echo "")
        else
            PID=$(netstat -tlnp 2>/dev/null | grep ":$port " | awk '{print $7}' | cut -d'/' -f1 | head -1 || echo "")
        fi
    fi

    if [ -z "$PID" ]; then
        echo -e "${RED}Error:${NC} No process found listening on port $port"
        echo -e "Make sure node-$node_num is running first."
        exit 1
    fi

    # Get process name
    PROCESS_NAME=$(ps -p $PID -o comm= 2>/dev/null || echo "unknown")

    echo -e "${GREEN}✓${NC} Found process: ${BLUE}$PROCESS_NAME${NC} (PID: ${BLUE}$PID${NC})"
    echo ""

    # Pause the process
    echo -e "${YELLOW}Pausing process (SIGSTOP)...${NC}"
    if ! kill -STOP $PID 2>/dev/null; then
        echo -e "${RED}Error:${NC} Failed to pause process. You may need sudo privileges."
        exit 1
    fi

    echo -e "${GREEN}✓${NC} Process paused - node-$node_num is now unreachable"
    echo -e "${YELLOW}Waiting $duration seconds...${NC}"
    echo ""

    # Show countdown
    for ((i=duration; i>0; i--)); do
        printf "\r${BLUE}Time remaining: %3d seconds${NC}" "$i"
        sleep 1
    done

    echo ""
    echo ""

    # Resume the process
    echo -e "${YELLOW}Resuming process (SIGCONT)...${NC}"
    if ! kill -CONT $PID 2>/dev/null; then
        echo -e "${YELLOW}Warning:${NC} Failed to resume process. It may have been terminated."
    else
        echo -e "${GREEN}✓${NC} Process resumed - node-$node_num is now accessible again"
    fi

    echo ""
    echo -e "${GREEN}=== Done! ===${NC}"
    echo -e "Local node ${GREEN}node-$node_num${NC} was unreachable for $duration seconds and is now accessible again."
}

# Main execution
if [ "$REMOTE_MODE" = true ]; then
    block_remote_node "$NODE_NUM" "$DURATION" "$PORTS"
else
    block_local_node "$NODE_NUM" "$DURATION" "$PORT"
fi
