#!/bin/bash

# Production Cluster Health Check Script
# Tests RQLite, IPFS, and IPFS Cluster connectivity and replication

# Note: We don't use 'set -e' here because we want to continue testing even if individual checks fail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Node IPs - Update these if needed
BOOTSTRAP="${BOOTSTRAP:-51.83.128.181}"
NODE1="${NODE1:-57.128.223.92}"
NODE2="${NODE2:-185.185.83.89}"

ALL_NODES=($BOOTSTRAP $NODE1 $NODE2)

# Counters
PASSED=0
FAILED=0
WARNINGS=0

# Helper functions
print_header() {
    echo ""
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}========================================${NC}"
}

print_test() {
    echo -e "${YELLOW}▶ $1${NC}"
}

print_pass() {
    echo -e "${GREEN}✓ $1${NC}"
    PASSED=$((PASSED + 1))
}

print_fail() {
    echo -e "${RED}✗ $1${NC}"
    FAILED=$((FAILED + 1))
}

print_warn() {
    echo -e "${YELLOW}⚠ $1${NC}"
    WARNINGS=$((WARNINGS + 1))
}

print_info() {
    echo -e "  $1"
}

# Test functions
test_rqlite_status() {
    print_header "1. RQLITE CLUSTER STATUS"
    
    local leader_found=false
    local follower_count=0
    local commit_indices=()
    
    for i in "${!ALL_NODES[@]}"; do
        local node="${ALL_NODES[$i]}"
        print_test "Testing RQLite on $node"
        
        if ! response=$(curl -s --max-time 5 http://$node:5001/status 2>/dev/null); then
            print_fail "Cannot connect to RQLite on $node:5001"
            continue
        fi
        
        local state=$(echo "$response" | jq -r '.store.raft.state // "unknown"')
        local num_peers=$(echo "$response" | jq -r '.store.raft.num_peers // 0')
        local commit_index=$(echo "$response" | jq -r '.store.raft.commit_index // 0')
        local last_contact=$(echo "$response" | jq -r '.store.raft.last_contact // "N/A"')
        local config=$(echo "$response" | jq -r '.store.raft.latest_configuration // "[]"')
        local node_count=$(echo "$config" | grep -o "Address" | wc -l | tr -d ' ')
        
        commit_indices+=($commit_index)
        
        print_info "State: $state | Peers: $num_peers | Commit Index: $commit_index | Cluster Nodes: $node_count"
        
        # Check state
        if [ "$state" = "Leader" ]; then
            leader_found=true
            print_pass "Node $node is the Leader"
        elif [ "$state" = "Follower" ]; then
            follower_count=$((follower_count + 1))
            # Check last contact
            if [ "$last_contact" != "N/A" ] && [ "$last_contact" != "0" ]; then
                print_pass "Node $node is a Follower (last contact: $last_contact)"
            else
                print_warn "Node $node is Follower but last_contact is $last_contact"
            fi
        else
            print_fail "Node $node has unexpected state: $state"
        fi
        
        # Check peer count
        if [ "$num_peers" = "2" ]; then
            print_pass "Node $node has correct peer count: 2"
        else
            print_fail "Node $node has incorrect peer count: $num_peers (expected 2)"
        fi
        
        # Check cluster configuration
        if [ "$node_count" = "3" ]; then
            print_pass "Node $node sees all 3 cluster members"
        else
            print_fail "Node $node only sees $node_count cluster members (expected 3)"
        fi
        
        echo ""
    done
    
    # Check for exactly 1 leader
    if [ "$leader_found" = true ] && [ "$follower_count" = "2" ]; then
        print_pass "Cluster has 1 Leader and 2 Followers ✓"
    else
        print_fail "Invalid cluster state (Leader found: $leader_found, Followers: $follower_count)"
    fi
    
    # Check commit index sync
    if [ ${#commit_indices[@]} -eq 3 ]; then
        local first="${commit_indices[0]}"
        local all_same=true
        for idx in "${commit_indices[@]}"; do
            if [ "$idx" != "$first" ]; then
                all_same=false
                break
            fi
        done
        
        if [ "$all_same" = true ]; then
            print_pass "All nodes have synced commit index: $first"
        else
            print_warn "Commit indices differ: ${commit_indices[*]} (might be normal if writes are happening)"
        fi
    fi
}

test_rqlite_replication() {
    print_header "2. RQLITE REPLICATION TEST"
    
    print_test "Creating test table and inserting data on leader ($BOOTSTRAP)"
    
    # Create table
    if ! response=$(curl -s --max-time 5 -XPOST "http://$BOOTSTRAP:5001/db/execute" \
        -H "Content-Type: application/json" \
        -d '[["CREATE TABLE IF NOT EXISTS test_cluster_health (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp TEXT, node TEXT, value TEXT)"]]' 2>/dev/null); then
        print_fail "Failed to create table"
        return
    fi
    
    if echo "$response" | jq -e '.results[0].error' >/dev/null 2>&1; then
        local error=$(echo "$response" | jq -r '.results[0].error')
        if [[ "$error" != "table test_cluster_health already exists" ]]; then
            print_fail "Table creation error: $error"
            return
        fi
    fi
    print_pass "Table exists"
    
    # Insert test data
    local test_value="test_$(date +%s)"
    if ! response=$(curl -s --max-time 5 -XPOST "http://$BOOTSTRAP:5001/db/execute" \
        -H "Content-Type: application/json" \
        -d "[
            [\"INSERT INTO test_cluster_health (timestamp, node, value) VALUES (datetime('now'), 'bootstrap', '$test_value')\"]
        ]" 2>/dev/null); then
        print_fail "Failed to insert data"
        return
    fi
    
    if echo "$response" | jq -e '.results[0].error' >/dev/null 2>&1; then
        local error=$(echo "$response" | jq -r '.results[0].error')
        print_fail "Insert error: $error"
        return
    fi
    print_pass "Data inserted: $test_value"
    
    # Wait for replication
    print_info "Waiting 2 seconds for replication..."
    sleep 2
    
    # Query from all nodes
    for node in "${ALL_NODES[@]}"; do
        print_test "Reading from $node"
        
        if ! response=$(curl -s --max-time 5 -XPOST "http://$node:5001/db/query?level=weak" \
            -H "Content-Type: application/json" \
            -d "[\"SELECT * FROM test_cluster_health WHERE value = '$test_value' LIMIT 1\"]" 2>/dev/null); then
            print_fail "Failed to query from $node"
            continue
        fi
        
        if echo "$response" | jq -e '.results[0].error' >/dev/null 2>&1; then
            local error=$(echo "$response" | jq -r '.results[0].error')
            print_fail "Query error on $node: $error"
            continue
        fi
        
        local row_count=$(echo "$response" | jq -r '.results[0].values | length // 0')
        if [ "$row_count" = "1" ]; then
            local retrieved_value=$(echo "$response" | jq -r '.results[0].values[0][3] // ""')
            if [ "$retrieved_value" = "$test_value" ]; then
                print_pass "Data replicated correctly to $node"
            else
                print_fail "Data mismatch on $node (got: $retrieved_value, expected: $test_value)"
            fi
        else
            print_fail "Expected 1 row from $node, got $row_count"
        fi
    done
}

test_ipfs_status() {
    print_header "3. IPFS DAEMON STATUS"
    
    for node in "${ALL_NODES[@]}"; do
        print_test "Testing IPFS on $node"
        
        if ! response=$(curl -s --max-time 5 -X POST http://$node:4501/api/v0/id 2>/dev/null); then
            print_fail "Cannot connect to IPFS on $node:4501"
            continue
        fi
        
        local peer_id=$(echo "$response" | jq -r '.ID // "unknown"')
        local addr_count=$(echo "$response" | jq -r '.Addresses | length // 0')
        local agent=$(echo "$response" | jq -r '.AgentVersion // "unknown"')
        
        if [ "$peer_id" != "unknown" ]; then
            print_pass "IPFS running on $node (ID: ${peer_id:0:12}...)"
            print_info "Agent: $agent | Addresses: $addr_count"
        else
            print_fail "IPFS not responding correctly on $node"
        fi
    done
}

test_ipfs_swarm() {
    print_header "4. IPFS SWARM CONNECTIVITY"
    
    for node in "${ALL_NODES[@]}"; do
        print_test "Checking IPFS swarm peers on $node"
        
        if ! response=$(curl -s --max-time 5 -X POST http://$node:4501/api/v0/swarm/peers 2>/dev/null); then
            print_fail "Failed to get swarm peers from $node"
            continue
        fi
        
        local peer_count=$(echo "$response" | jq -r '.Peers | length // 0')
        
        if [ "$peer_count" = "2" ]; then
            print_pass "Node $node connected to 2 IPFS peers"
        elif [ "$peer_count" -gt "0" ]; then
            print_warn "Node $node connected to $peer_count IPFS peers (expected 2)"
        else
            print_fail "Node $node has no IPFS swarm peers"
        fi
    done
}

test_ipfs_cluster_status() {
    print_header "5. IPFS CLUSTER STATUS"
    
    for node in "${ALL_NODES[@]}"; do
        print_test "Testing IPFS Cluster on $node"
        
        if ! response=$(curl -s --max-time 5 http://$node:9094/id 2>/dev/null); then
            print_fail "Cannot connect to IPFS Cluster on $node:9094"
            continue
        fi
        
        local cluster_id=$(echo "$response" | jq -r '.id // "unknown"')
        local cluster_peers=$(echo "$response" | jq -r '.cluster_peers | length // 0')
        local version=$(echo "$response" | jq -r '.version // "unknown"')
        
        if [ "$cluster_id" != "unknown" ]; then
            print_pass "IPFS Cluster running on $node (ID: ${cluster_id:0:12}...)"
            print_info "Version: $version | Cluster Peers: $cluster_peers"
            
            if [ "$cluster_peers" = "3" ]; then
                print_pass "Node $node sees all 3 cluster peers"
            else
                print_warn "Node $node sees $cluster_peers cluster peers (expected 3)"
            fi
        else
            print_fail "IPFS Cluster not responding correctly on $node"
        fi
    done
}

test_ipfs_cluster_pins() {
    print_header "6. IPFS CLUSTER PIN CONSISTENCY"
    
    local pin_counts=()
    
    for node in "${ALL_NODES[@]}"; do
        print_test "Checking pins on $node"
        
        if ! response=$(curl -s --max-time 5 http://$node:9094/pins 2>/dev/null); then
            print_fail "Failed to get pins from $node"
            pin_counts+=(0)
            continue
        fi
        
        local pin_count=$(echo "$response" | jq -r 'length // 0')
        pin_counts+=($pin_count)
        print_pass "Node $node has $pin_count pins"
    done
    
    # Check if all nodes have same pin count
    if [ ${#pin_counts[@]} -eq 3 ]; then
        local first="${pin_counts[0]}"
        local all_same=true
        for count in "${pin_counts[@]}"; do
            if [ "$count" != "$first" ]; then
                all_same=false
                break
            fi
        done
        
        if [ "$all_same" = true ]; then
            print_pass "All nodes have consistent pin count: $first"
        else
            print_warn "Pin counts differ: ${pin_counts[*]} (might be syncing)"
        fi
    fi
}

print_summary() {
    print_header "TEST SUMMARY"
    
    echo ""
    echo -e "${GREEN}Passed:   $PASSED${NC}"
    echo -e "${YELLOW}Warnings: $WARNINGS${NC}"
    echo -e "${RED}Failed:   $FAILED${NC}"
    echo ""
    
    if [ $FAILED -eq 0 ]; then
        echo -e "${GREEN}🎉 All critical tests passed! Cluster is healthy.${NC}"
        exit 0
    elif [ $FAILED -le 2 ]; then
        echo -e "${YELLOW}⚠️  Some tests failed. Review the output above.${NC}"
        exit 1
    else
        echo -e "${RED}❌ Multiple failures detected. Cluster needs attention.${NC}"
        exit 2
    fi
}

# Main execution
main() {
    echo ""
    echo -e "${BLUE}╔════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║  DEBROS Production Cluster Health Check   ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════╝${NC}"
    echo ""
    echo "Testing cluster:"
    echo "  Bootstrap: $BOOTSTRAP"
    echo "  Node 1:    $NODE1"
    echo "  Node 2:    $NODE2"
    
    test_rqlite_status
    test_rqlite_replication
    test_ipfs_status
    test_ipfs_swarm
    test_ipfs_cluster_status
    test_ipfs_cluster_pins
    print_summary
}

# Run main
main

