#!/bin/bash
# install-coredns.sh - Install and configure CoreDNS on Orama Network nodes
set -euo pipefail

COREDNS_VERSION="${COREDNS_VERSION:-1.11.1}"
ARCH="linux_amd64"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/coredns"
DATA_DIR="/var/lib/coredns"
USER="debros"

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
    log_error "User '$USER' does not exist. Please create it first."
    exit 1
fi

log_info "Installing CoreDNS $COREDNS_VERSION..."

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

# Copy Corefile if provided
if [ -f "./configs/coredns/Corefile" ]; then
    log_info "Copying Corefile configuration..."
    cp ./configs/coredns/Corefile "$CONFIG_DIR/Corefile"
else
    log_warn "Corefile not found in ./configs/coredns/Corefile"
    log_warn "Please copy your Corefile to $CONFIG_DIR/Corefile manually"
fi

# Install systemd service
log_info "Installing systemd service..."
if [ -f "./configs/coredns/coredns.service" ]; then
    cp ./configs/coredns/coredns.service /etc/systemd/system/
    systemctl daemon-reload
    log_info "Systemd service installed"
else
    log_warn "Service file not found in ./configs/coredns/coredns.service"
fi

# Verify installation
log_info "Verifying installation..."
if command -v coredns >/dev/null 2>&1; then
    VERSION_OUTPUT=$(coredns -version 2>&1 | head -1)
    log_info "Installed: $VERSION_OUTPUT"
else
    log_error "CoreDNS installation verification failed"
    exit 1
fi

# Firewall configuration reminder
log_warn "IMPORTANT: Configure firewall to allow DNS traffic"
log_warn "  - UDP/TCP port 53 (DNS)"
log_warn "  - TCP port 8080 (health check)"
log_warn "  - TCP port 9153 (metrics)"
echo
log_warn "Example firewall rules:"
log_warn "  sudo ufw allow 53/tcp"
log_warn "  sudo ufw allow 53/udp"
log_warn "  sudo ufw allow 8080/tcp"
log_warn "  sudo ufw allow 9153/tcp"

# Service management instructions
echo
log_info "Installation complete!"
echo
log_info "To configure CoreDNS:"
log_info "  1. Edit $CONFIG_DIR/Corefile"
log_info "  2. Ensure RQLite is running and accessible"
echo
log_info "To start CoreDNS:"
log_info "  sudo systemctl enable coredns"
log_info "  sudo systemctl start coredns"
echo
log_info "To check status:"
log_info "  sudo systemctl status coredns"
log_info "  sudo journalctl -u coredns -f"
echo
log_info "To test DNS:"
log_info "  dig @localhost test.orama.network"

# Cleanup
rm -f /tmp/coredns.tgz

log_info "Done!"
