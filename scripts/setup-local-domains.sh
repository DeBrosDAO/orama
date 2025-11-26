#!/bin/bash

# Setup local domains for DeBros Network development
# Adds entries to /etc/hosts for node-1.local through node-5.local
# Maps them to 127.0.0.1 for local development

set -e

HOSTS_FILE="/etc/hosts"
NODES=("node-1" "node-2" "node-3" "node-4" "node-5")

# Check if we have sudo access
if [ "$EUID" -ne 0 ]; then
  echo "This script requires sudo to modify /etc/hosts"
  echo "Please run: sudo bash scripts/setup-local-domains.sh"
  exit 1
fi

# Function to add or update domain entry
add_domain() {
  local domain=$1
  local ip="127.0.0.1"
  
  # Check if domain already exists
  if grep -q "^[[:space:]]*$ip[[:space:]]\+$domain" "$HOSTS_FILE"; then
    echo "✓ $domain already configured"
    return 0
  fi
  
  # Add domain to /etc/hosts
  echo "$ip	$domain" >> "$HOSTS_FILE"
  echo "✓ Added $domain -> $ip"
}

echo "Setting up local domains for DeBros Network..."
echo ""

# Add each node domain
for node in "${NODES[@]}"; do
  add_domain "${node}.local"
done

echo ""
echo "✓ Local domains configured successfully!"
echo ""
echo "You can now access nodes via:"
for node in "${NODES[@]}"; do
  echo "  - ${node}.local (HTTP Gateway)"
done

echo ""
echo "Example: curl http://node-1.local:8080/rqlite/http/db/status"

