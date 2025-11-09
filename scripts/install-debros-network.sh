#!/bin/bash

# DeBros Network Installation Script
# Downloads network-cli from GitHub releases and runs the new 'network-cli prod install' flow
# 
# Supported: Ubuntu 20.04+, Debian 11+
# 
# Usage:
#   curl -fsSL https://install.debros.network | bash
#   OR
#   bash scripts/install-debros-network.sh
#   OR with specific flags:
#   bash scripts/install-debros-network.sh --bootstrap
#   bash scripts/install-debros-network.sh --vps-ip 1.2.3.4 --peers /ip4/1.2.3.4/tcp/4001/p2p/Qm...
#   bash scripts/install-debros-network.sh --domain example.com

set -e
trap 'error "An error occurred. Installation aborted."; exit 1' ERR

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BLUE='\033[38;2;2;128;175m'
YELLOW='\033[1;33m'
NOCOLOR='\033[0m'

# Configuration
GITHUB_REPO="DeBrosOfficial/network"
GITHUB_API="https://api.github.com/repos/$GITHUB_REPO"
INSTALL_DIR="/usr/local/bin"

log() { echo -e "${CYAN}[$(date '+%Y-%m-%d %H:%M:%S')]${NOCOLOR} $1"; }
error() { echo -e "${RED}[ERROR]${NOCOLOR} $1" >&2; }
success() { echo -e "${GREEN}[SUCCESS]${NOCOLOR} $1"; }
warning() { echo -e "${YELLOW}[WARNING]${NOCOLOR} $1" >&2; }

display_banner() {
    echo -e "${BLUE}========================================================================${NOCOLOR}"
    echo -e "${CYAN}
    ____       ____                   _   _      _                      _
   |  _ \\  ___| __ ) _ __ ___  ___   | \\ | | ___| |___      _____  _ __| | __
   | | | |/ _ \\  _ \\|  __/ _ \\/ __|  |  \\| |/ _ \\ __\\ \\ /\\ / / _ \\|  __| |/ /
   | |_| |  __/ |_) | | | (_) \\__ \\  | |\\  |  __/ |_ \\ V  V / (_) | |  |   <
   |____/ \\___|____/|_|  \\___/|___/  |_| \\_|\\___|\\__| \\_/\\_/ \\___/|_|  |_|\\_\\
${NOCOLOR}"
    echo -e "${BLUE}========================================================================${NOCOLOR}"
    echo -e "${GREEN}                    Production Installation                           ${NOCOLOR}"
    echo -e "${BLUE}========================================================================${NOCOLOR}"
}

detect_os() {
    if [ ! -f /etc/os-release ]; then
        error "Cannot detect operating system"
        exit 1
    fi
    
    . /etc/os-release
    OS=$ID
    VERSION=$VERSION_ID
    
    # Support Debian and Ubuntu
    case $OS in
        ubuntu|debian)
            log "Detected OS: $OS ${VERSION:-unknown}"
            ;;
        *)
            warning "Unsupported operating system: $OS (may not work)"
            ;;
    esac
}

check_architecture() {
    ARCH=$(uname -m)
    case $ARCH in
        x86_64)
            GITHUB_ARCH="amd64"
            ;;
        aarch64|arm64)
            GITHUB_ARCH="arm64"
            ;;
        *)
            error "Unsupported architecture: $ARCH"
            echo -e "${YELLOW}Supported: x86_64, aarch64/arm64${NOCOLOR}"
            exit 1
            ;;
    esac
    log "Architecture: $ARCH (using $GITHUB_ARCH)"
}

check_root() {
    if [[ $EUID -ne 0 ]]; then
        error "This script must be run as root"
        echo -e "${YELLOW}Please run with sudo:${NOCOLOR}"
        echo -e "${CYAN}  sudo bash <(curl -fsSL https://install.debros.network)${NOCOLOR}"
        exit 1
    fi
}

get_latest_release() {
    log "Fetching latest release..."
    
    if command -v jq &>/dev/null; then
        LATEST_RELEASE=$(curl -fsSL -H "Accept: application/vnd.github+json" "$GITHUB_API/releases" | \
            jq -r '.[] | select(.prerelease == false and .draft == false) | .tag_name' | head -1)
    else
        LATEST_RELEASE=$(curl -fsSL "$GITHUB_API/releases" | \
            grep -v "prerelease.*true" | \
            grep -v "draft.*true" | \
            grep '"tag_name"' | \
            head -1 | \
            cut -d'"' -f4)
    fi
    
    if [ -z "$LATEST_RELEASE" ]; then
        error "Could not determine latest release version"
        exit 1
    fi
    
    log "Latest release: $LATEST_RELEASE"
}

download_and_install_cli() {
    BINARY_NAME="network-cli_${LATEST_RELEASE#v}_linux_${GITHUB_ARCH}"
    DOWNLOAD_URL="$GITHUB_REPO/releases/download/$LATEST_RELEASE/$BINARY_NAME"
    
    log "Downloading network-cli from GitHub releases..."
    if ! curl -fsSL -o /tmp/network-cli "https://github.com/$DOWNLOAD_URL"; then
        error "Failed to download network-cli"
        exit 1
    fi
    
    chmod +x /tmp/network-cli
    
    log "Installing network-cli to $INSTALL_DIR..."
    mv /tmp/network-cli "$INSTALL_DIR/network-cli"
    
    success "network-cli installed successfully"
}

# Main flow
display_banner

# Check prerequisites
check_root
detect_os
check_architecture

# Download and install
get_latest_release
download_and_install_cli

# Show next steps
echo ""
echo -e "${GREEN}Installation complete!${NOCOLOR}"
echo ""
echo -e "${CYAN}Next, run the production setup:${NOCOLOR}"
echo ""
echo "Bootstrap node (first node):"
echo -e "  ${BLUE}sudo network-cli prod install --bootstrap${NOCOLOR}"
echo ""
echo "Secondary node (join existing cluster):"
echo -e "  ${BLUE}sudo network-cli prod install --vps-ip <bootstrap_ip> --peers <multiaddr>${NOCOLOR}"
echo ""
echo "With HTTPS/domain:"
echo -e "  ${BLUE}sudo network-cli prod install --bootstrap --domain example.com${NOCOLOR}"
echo ""
echo "For more help:"
echo -e "  ${BLUE}network-cli prod --help${NOCOLOR}"
echo ""
