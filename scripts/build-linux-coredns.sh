#!/bin/bash
# Build CoreDNS with rqlite plugin for linux/amd64
# Outputs to bin-linux/coredns
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="$PROJECT_ROOT/bin-linux"
BUILD_DIR="/tmp/coredns-build-linux"

mkdir -p "$OUTPUT_DIR"

# Clean up previous build
rm -rf "$BUILD_DIR"

# Clone CoreDNS
echo "Cloning CoreDNS v1.12.0..."
git clone --depth 1 --branch v1.12.0 https://github.com/coredns/coredns.git "$BUILD_DIR"

# Copy rqlite plugin
echo "Copying rqlite plugin..."
mkdir -p "$BUILD_DIR/plugin/rqlite"
cp "$PROJECT_ROOT/pkg/coredns/rqlite/"*.go "$BUILD_DIR/plugin/rqlite/"

# Write plugin.cfg
cat > "$BUILD_DIR/plugin.cfg" << 'EOF'
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
rqlite:rqlite
EOF

# Build
cd "$BUILD_DIR"
echo "Adding dependencies..."
go get github.com/miekg/dns@latest
go get go.uber.org/zap@latest
go mod tidy

echo "Generating plugin code..."
go generate

echo "Building CoreDNS binary..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w" -trimpath -o coredns

# Copy output
cp "$BUILD_DIR/coredns" "$OUTPUT_DIR/coredns"

# Cleanup
rm -rf "$BUILD_DIR"
echo "✓ CoreDNS built: bin-linux/coredns"
