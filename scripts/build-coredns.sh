#!/bin/bash
set -e

# Build custom CoreDNS binary with RQLite plugin
# This script compiles CoreDNS with the custom RQLite plugin

COREDNS_VERSION="1.11.1"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
COREDNS_DIR="/tmp/coredns-build"

echo "Building CoreDNS v${COREDNS_VERSION} with RQLite plugin..."

# Clean previous build
rm -rf "$COREDNS_DIR"
mkdir -p "$COREDNS_DIR"

# Clone CoreDNS
echo "Cloning CoreDNS..."
cd "$COREDNS_DIR"
git clone --depth 1 --branch v${COREDNS_VERSION} https://github.com/coredns/coredns.git
cd coredns

# Create plugin.cfg with RQLite plugin
echo "Configuring plugins..."
cat > plugin.cfg <<EOF
# Standard CoreDNS plugins
metadata:metadata
cancel:cancel
tls:tls
reload:reload
nsid:nsid
bufsize:bufsize
root:root
bind:bind
debug:debug
trace:trace
ready:ready
health:health
pprof:pprof
prometheus:metrics
errors:errors
log:log
dnstap:dnstap
local:local
dns64:dns64
acl:acl
any:any
chaos:chaos
loadbalance:loadbalance
cache:cache
rewrite:rewrite
header:header
dnssec:dnssec
autopath:autopath
minimal:minimal
template:template
transfer:transfer
hosts:hosts
route53:route53
azure:azure
clouddns:clouddns
k8s_external:k8s_external
kubernetes:kubernetes
file:file
auto:auto
secondary:secondary
loop:loop
forward:forward
grpc:grpc
erratic:erratic
whoami:whoami
on:github.com/coredns/caddy/onevent
sign:sign
view:view

# Custom RQLite plugin
rqlite:github.com/DeBrosOfficial/network/pkg/coredns/rqlite
EOF

# Copy RQLite plugin to CoreDNS
echo "Copying RQLite plugin..."
mkdir -p plugin/rqlite
cp -r "$PROJECT_ROOT/pkg/coredns/rqlite/"* plugin/rqlite/

# Update go.mod to include our dependencies
echo "Updating dependencies..."
go get github.com/rqlite/rqlite-go@latest
go get github.com/coredns/coredns@v${COREDNS_VERSION}
go mod tidy

# Build CoreDNS
echo "Building CoreDNS binary..."
make

# Copy binary to project
echo "Copying binary to project..."
cp coredns "$PROJECT_ROOT/bin/coredns-custom"
chmod +x "$PROJECT_ROOT/bin/coredns-custom"

echo ""
echo "✅ CoreDNS built successfully!"
echo "Binary location: $PROJECT_ROOT/bin/coredns-custom"
echo ""
echo "To deploy:"
echo "  1. Copy binary to /usr/local/bin/coredns on each nameserver node"
echo "  2. Copy configs/coredns/Corefile to /etc/coredns/Corefile"
echo "  3. Start CoreDNS: sudo systemctl start coredns"
echo ""
