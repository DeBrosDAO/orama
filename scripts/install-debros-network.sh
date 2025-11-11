#!/bin/bash

# DeBros Network Installation Script
# Downloads dbn from GitHub releases and runs the new 'dbn prod install' flow
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
        # Get the latest release (including pre-releases/nightly)
        LATEST_RELEASE=$(curl -fsSL -H "Accept: application/vnd.github+json" "$GITHUB_API/releases" | \
            jq -r '.[0] | .tag_name')
    else
        LATEST_RELEASE=$(curl -fsSL "$GITHUB_API/releases" | \
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
    BINARY_NAME="debros-network_${LATEST_RELEASE#v}_linux_${GITHUB_ARCH}.tar.gz"
    DOWNLOAD_URL="$GITHUB_REPO/releases/download/$LATEST_RELEASE/$BINARY_NAME"
    
    log "Downloading dbn from GitHub releases..."
    log "URL: https://github.com/$DOWNLOAD_URL"
    
    # Clean up any stale binaries
    rm -f /tmp/network-cli /tmp/dbn.tar.gz "$INSTALL_DIR/dbn"
    
    if ! curl -fsSL -o /tmp/dbn.tar.gz "https://github.com/$DOWNLOAD_URL"; then
        error "Failed to download dbn"
        exit 1
    fi
    
    # Verify the download was successful
    if [ ! -f /tmp/dbn.tar.gz ]; then
        error "Download file not found"
        exit 1
    fi
    
    log "Extracting dbn..."
    # Extract to /tmp
    tar -xzf /tmp/dbn.tar.gz -C /tmp/
    
    # Check for extracted binary (could be named network-cli or dbn)
    EXTRACTED_BINARY=""
    if [ -f /tmp/network-cli ]; then
        EXTRACTED_BINARY="/tmp/network-cli"
    elif [ -f /tmp/dbn ]; then
        EXTRACTED_BINARY="/tmp/dbn"
    else
        error "Failed to extract binary (neither network-cli nor dbn found)"
        ls -la /tmp/ | grep -E "(network|cli|dbn)"
        exit 1
    fi
    
    chmod +x "$EXTRACTED_BINARY"
    
    log "Installing dbn to $INSTALL_DIR..."
    # Always rename to dbn during installation
    mv "$EXTRACTED_BINARY" "$INSTALL_DIR/dbn"
    
    # Sanity check: verify the installed binary is functional and reports correct version
    if ! "$INSTALL_DIR/dbn" version &>/dev/null; then
        error "Installed dbn failed sanity check (version command failed)"
        rm -f "$INSTALL_DIR/dbn"
        exit 1
    fi
    
    # Clean up
    rm -f /tmp/dbn.tar.gz
    
    success "dbn installed successfully"
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
echo "Bootstrap node (first node, main branch):"
echo -e "  ${BLUE}sudo dbn prod install --bootstrap${NOCOLOR}"
echo ""
echo "Bootstrap node (nightly branch):"
echo -e "  ${BLUE}sudo dbn prod install --bootstrap --branch nightly${NOCOLOR}"
echo ""
echo "Secondary node (join existing cluster):"
echo -e "  ${BLUE}sudo dbn prod install --vps-ip <bootstrap_ip> --peers <multiaddr>${NOCOLOR}"
echo ""
echo "With HTTPS/domain:"
echo -e "  ${BLUE}sudo dbn prod install --bootstrap --domain example.com${NOCOLOR}"
echo ""
echo "For more help:"
echo -e "  ${BLUE}dbn prod --help${NOCOLOR}"
echo ""
