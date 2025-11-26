#!/bin/bash

# Test local domain routing for DeBros Network
# Validates that all HTTP gateway routes are working

set -e

NODES=("1" "2" "3" "4" "5")
GATEWAY_PORTS=(8080 8081 8082 8083 8084)

# Color codes
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Counters
PASSED=0
FAILED=0

# Test a single endpoint
test_endpoint() {
  local node=$1
  local port=$2
  local path=$3
  local description=$4
  
  local url="http://node-${node}.local:${port}${path}"
  
  printf "Testing %-50s ... " "$description"
  
  if curl -s -f "$url" > /dev/null 2>&1; then
    echo -e "${GREEN}✓ PASS${NC}"
    ((PASSED++))
    return 0
  else
    echo -e "${RED}✗ FAIL${NC}"
    ((FAILED++))
    return 1
  fi
}

echo "=========================================="
echo "DeBros Network Local Domain Tests"
echo "=========================================="
echo ""

# Test each node's HTTP gateway
for i in "${!NODES[@]}"; do
  node=${NODES[$i]}
  port=${GATEWAY_PORTS[$i]}
  
  echo "Testing node-${node}.local (port ${port}):"
  
  # Test health endpoint
  test_endpoint "$node" "$port" "/health" "Node-${node} health check"
  
  # Test RQLite HTTP endpoint
  test_endpoint "$node" "$port" "/rqlite/http/db/execute" "Node-${node} RQLite HTTP"
  
  # Test IPFS API endpoint (may fail if IPFS not running, but at least connection should work)
  test_endpoint "$node" "$port" "/ipfs/api/v0/version" "Node-${node} IPFS API" || true
  
  # Test Cluster API endpoint (may fail if Cluster not running, but at least connection should work)
  test_endpoint "$node" "$port" "/cluster/health" "Node-${node} Cluster API" || true
  
  echo ""
done

# Summary
echo "=========================================="
echo "Test Results"
echo "=========================================="
echo -e "${GREEN}Passed: $PASSED${NC}"
echo -e "${RED}Failed: $FAILED${NC}"
echo ""

if [ $FAILED -eq 0 ]; then
  echo -e "${GREEN}✓ All tests passed!${NC}"
  exit 0
else
  echo -e "${YELLOW}⚠ Some tests failed (this is expected if services aren't running)${NC}"
  exit 1
fi

