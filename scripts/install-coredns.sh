#!/bin/bash
# install-coredns.sh - Install and configure CoreDNS for DeBros Network nodes
# This script sets up a simple wildcard DNS server for deployment subdomains
set -euo pipefail

COREDNS_VERSION="${COREDNS_VERSION:-1.11.1}"
ARCH="linux_amd64"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/coredns"
DATA_DIR="/var/lib/coredns"
USER="debros"

# Configuration - Override these with environment variables
DOMAIN="${DOMAIN:-dbrs.space}"
NODE_IP="${NODE_IP:-}"  # Auto-detected if not provided

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    log_error "This script must be run as root"
    exit 1
fi

# Check if debros user exists
if ! id -u "$USER" >/dev/null 2>&1; then
    log_warn "User '$USER' does not exist. Creating..."
    useradd -r -m -s /bin/bash "$USER" || true
fi

# Auto-detect node IP if not provided
if [ -z "$NODE_IP" ]; then
    NODE_IP=$(hostname -I | awk '{print $1}')
    log_info "Auto-detected node IP: $NODE_IP"
fi

if [ -z "$NODE_IP" ]; then
    log_error "Could not detect node IP. Please set NODE_IP environment variable."
    exit 1
fi

log_info "Installing CoreDNS $COREDNS_VERSION for domain $DOMAIN..."

# Disable systemd-resolved stub listener to free port 53
log_info "Configuring systemd-resolved..."
mkdir -p /etc/systemd/resolved.conf.d/
cat > /etc/systemd/resolved.conf.d/disable-stub.conf << 'EOF'
[Resolve]
DNSStubListener=no
EOF
systemctl restart systemd-resolved || true

# Download CoreDNS
cd /tmp
DOWNLOAD_URL="https://github.com/coredns/coredns/releases/download/v${COREDNS_VERSION}/coredns_${COREDNS_VERSION}_${ARCH}.tgz"
log_info "Downloading from $DOWNLOAD_URL"

curl -sSL "$DOWNLOAD_URL" -o coredns.tgz
if [ $? -ne 0 ]; then
    log_error "Failed to download CoreDNS"
    exit 1
fi

# Extract and install
log_info "Extracting CoreDNS..."
tar -xzf coredns.tgz
chmod +x coredns
mv coredns "$INSTALL_DIR/"

log_info "CoreDNS installed to $INSTALL_DIR/coredns"

# Create directories
log_info "Creating directories..."
mkdir -p "$CONFIG_DIR"
mkdir -p "$DATA_DIR"
chown -R "$USER:$USER" "$DATA_DIR"

# Create Corefile for simple wildcard DNS
log_info "Creating Corefile..."
cat > "$CONFIG_DIR/Corefile" << EOF
# CoreDNS configuration for $DOMAIN
# Serves wildcard DNS for deployment subdomains

$DOMAIN {
    file $CONFIG_DIR/db.$DOMAIN
    log
    errors
}

# Forward all other queries to upstream DNS
. {
    forward . 8.8.8.8 8.8.4.4 1.1.1.1
    cache 300
    errors
}
EOF

# Create zone file
log_info "Creating zone file for $DOMAIN..."
SERIAL=$(date +%Y%m%d%H)
cat > "$CONFIG_DIR/db.$DOMAIN" << EOF
\$ORIGIN $DOMAIN.
\$TTL 300

@       IN      SOA     ns1.$DOMAIN. admin.$DOMAIN. (
                        $SERIAL  ; Serial
                        3600     ; Refresh
                        1800     ; Retry
                        604800   ; Expire
                        300 )    ; Negative TTL

; Nameservers
@       IN      NS      ns1.$DOMAIN.
@       IN      NS      ns2.$DOMAIN.
@       IN      NS      ns3.$DOMAIN.

; Glue records - update these with actual nameserver IPs
ns1     IN      A       $NODE_IP
ns2     IN      A       $NODE_IP
ns3     IN      A       $NODE_IP

; Root domain
@       IN      A       $NODE_IP

; Wildcard for all subdomains (deployments)
*       IN      A       $NODE_IP
EOF

# Create systemd service
log_info "Creating systemd service..."
cat > /etc/systemd/system/coredns.service << EOF
[Unit]
Description=CoreDNS DNS Server
Documentation=https://coredns.io
After=network.target

[Service]
Type=simple
User=root
ExecStart=$INSTALL_DIR/coredns -conf $CONFIG_DIR/Corefile
Restart=on-failure
RestartSec=5

# Security hardening
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload

# Set up iptables redirect for port 80 -> gateway port 6001
log_info "Setting up port 80 redirect to gateway port 6001..."
iptables -t nat -C PREROUTING -p tcp --dport 80 -j REDIRECT --to-port 6001 2>/dev/null || \
    iptables -t nat -A PREROUTING -p tcp --dport 80 -j REDIRECT --to-port 6001

# Make iptables rules persistent
mkdir -p /etc/network/if-pre-up.d/
cat > /etc/network/if-pre-up.d/iptables-redirect << 'EOF'
#!/bin/sh
iptables -t nat -C PREROUTING -p tcp --dport 80 -j REDIRECT --to-port 6001 2>/dev/null || \
    iptables -t nat -A PREROUTING -p tcp --dport 80 -j REDIRECT --to-port 6001
EOF
chmod +x /etc/network/if-pre-up.d/iptables-redirect

# Configure firewall
log_info "Configuring firewall..."
if command -v ufw >/dev/null 2>&1; then
    ufw allow 53/tcp >/dev/null 2>&1 || true
    ufw allow 53/udp >/dev/null 2>&1 || true
    ufw allow 80/tcp >/dev/null 2>&1 || true
    log_info "Firewall rules added for ports 53 (DNS) and 80 (HTTP)"
else
    log_warn "UFW not found. Please manually configure firewall for ports 53 and 80"
fi

# Enable and start CoreDNS
log_info "Starting CoreDNS..."
systemctl enable coredns
systemctl start coredns

# Verify installation
sleep 2
if systemctl is-active --quiet coredns; then
    log_info "CoreDNS is running"
else
    log_error "CoreDNS failed to start. Check: journalctl -u coredns"
    exit 1
fi

# Test DNS resolution
log_info "Testing DNS resolution..."
if dig @localhost test.$DOMAIN +short | grep -q "$NODE_IP"; then
    log_info "DNS test passed: test.$DOMAIN resolves to $NODE_IP"
else
    log_warn "DNS test failed or returned unexpected result"
fi

# Cleanup
rm -f /tmp/coredns.tgz

echo
log_info "============================================"
log_info "CoreDNS installation complete!"
log_info "============================================"
echo
log_info "Configuration:"
log_info "  Domain: $DOMAIN"
log_info "  Node IP: $NODE_IP"
log_info "  Corefile: $CONFIG_DIR/Corefile"
log_info "  Zone file: $CONFIG_DIR/db.$DOMAIN"
echo
log_info "Commands:"
log_info "  Status:  sudo systemctl status coredns"
log_info "  Logs:    sudo journalctl -u coredns -f"
log_info "  Test:    dig @localhost anything.$DOMAIN"
echo
log_info "Note: Update the zone file with other nameserver IPs for redundancy:"
log_info "  sudo vi $CONFIG_DIR/db.$DOMAIN"
echo
log_info "Done!"
