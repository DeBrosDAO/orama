#!/bin/bash

# DeBros Network Installation Script
# Downloads network-cli from GitHub releases and runs interactive setup
# 
# Supported: Ubuntu 18.04+, Debian 10+
# 
# Usage:
#   curl -fsSL https://install.debros.network | bash
#   OR
#   bash scripts/install-debros-network.sh

set -e
trap 'echo -e "${RED}An error occurred. Installation aborted.${NOCOLOR}"; exit 1' ERR

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
error() { echo -e "${RED}[ERROR]${NOCOLOR} $1"; }
success() { echo -e "${GREEN}[SUCCESS]${NOCOLOR} $1"; }
warning() { echo -e "${YELLOW}[WARNING]${NOCOLOR} $1"; }

# REQUIRE INTERACTIVE MODE
if [ ! -t 0 ]; then
    error "This script requires an interactive terminal."
    echo -e ""
    echo -e "${YELLOW}Please run this script directly:${NOCOLOR}"
    echo -e "${CYAN}  bash <(curl -fsSL https://install.debros.network)${NOCOLOR}"
    echo -e ""
    exit 1
fi

# Check if running as root
if [[ $EUID -eq 0 ]]; then
    error "This script should NOT be run as root"
    echo -e "${YELLOW}Run as a regular user with sudo privileges:${NOCOLOR}"
    echo -e "${CYAN}  bash $0${NOCOLOR}"
    exit 1
fi

# Check for sudo
if ! command -v sudo &>/dev/null; then
    error "sudo command not found. Please ensure you have sudo privileges."
    exit 1
fi

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
    echo -e "${GREEN}                    Quick Install Script                               ${NOCOLOR}"
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
    
    # Only support Debian and Ubuntu
    case $OS in
        ubuntu|debian)
            log "Detected OS: $OS ${VERSION:-unknown}"
            ;;
        *)
            error "Unsupported operating system: $OS"
            echo -e "${YELLOW}This script only supports Ubuntu 18.04+ and Debian 10+${NOCOLOR}"
            exit 1
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

check_dependencies() {
    log "Checking required tools..."
    
    local missing_deps=()
    
    for cmd in curl tar; do
        if ! command -v $cmd &>/dev/null; then
            missing_deps+=("$cmd")
        fi
    done
    
    if [ ${#missing_deps[@]} -gt 0 ]; then
        log "Installing missing dependencies: ${missing_deps[*]}"
        sudo apt update
        sudo apt install -y "${missing_deps[@]}"
    fi
    
    success "All required tools available"
}

get_latest_release() {
    log "Fetching latest release information..."
    
    # Get latest release (exclude pre-releases and nightly)
    LATEST_RELEASE=$(curl -fsSL "$GITHUB_API/releases" | \
        grep -v "prerelease.*true" | \
        grep -v "draft.*true" | \
        grep '"tag_name"' | \
        head -1 | \
        cut -d'"' -f4)
    
    if [ -z "$LATEST_RELEASE" ]; then
        error "Could not determine latest release"
        exit 1
    fi
    
    log "Latest release: $LATEST_RELEASE"
}

download_and_install() {
    log "Downloading network-cli..."
    
    # Construct download URL
    DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/$LATEST_RELEASE/debros-network_${LATEST_RELEASE#v}_linux_${GITHUB_ARCH}.tar.gz"
    
    # Create temporary directory
    TEMP_DIR=$(mktemp -d)
    trap "rm -rf $TEMP_DIR" EXIT
    
    # Download
    log "Downloading from: $DOWNLOAD_URL"
    if ! curl -fsSL -o "$TEMP_DIR/network-cli.tar.gz" "$DOWNLOAD_URL"; then
        error "Failed to download network-cli"
        exit 1
    fi
    
    # Extract
    log "Extracting network-cli..."
    cd "$TEMP_DIR"
    tar xzf network-cli.tar.gz
    
    # Install
    log "Installing to $INSTALL_DIR..."
    sudo cp network-cli "$INSTALL_DIR/"
    sudo chmod +x "$INSTALL_DIR/network-cli"
    
    success "network-cli installed successfully"
}

verify_installation() {
    if command -v network-cli &>/dev/null; then
        INSTALLED_VERSION=$(network-cli version 2>/dev/null || echo "unknown")
        success "network-cli is ready: $INSTALLED_VERSION"
        return 0
    else
        error "network-cli not found in PATH"
        return 1
    fi
}

run_setup() {
    echo -e ""
    echo -e "${BLUE}========================================${NOCOLOR}"
    echo -e "${GREEN}Step 2: Run Interactive Setup${NOCOLOR}"
    echo -e "${BLUE}========================================${NOCOLOR}"
    echo -e ""
    
    log "The setup command will:"
    log "  • Create system user and directories"
    log "  • Install dependencies (RQLite, etc.)"
    log "  • Build DeBros binaries"
    log "  • Configure network settings"
    log "  • Create and start systemd services"
    echo -e ""
    
    echo -e "${YELLOW}Ready to run setup? This will prompt for configuration details.${NOCOLOR}"
    echo -n "Continue? (yes/no): "
    read -r CONTINUE_SETUP
    
    if [[ "$CONTINUE_SETUP" != "yes" && "$CONTINUE_SETUP" != "y" ]]; then
        echo -e ""
        success "network-cli installed successfully!"
        echo -e ""
        echo -e "${CYAN}To complete setup later, run:${NOCOLOR}"
        echo -e "${GREEN}  sudo network-cli setup${NOCOLOR}"
        echo -e ""
        return 0
    fi
    
    echo -e ""
    log "Running setup (requires sudo)..."
    sudo network-cli setup
}

show_completion() {
    echo -e ""
    echo -e "${BLUE}========================================================================${NOCOLOR}"
    success "DeBros Network installation complete!"
    echo -e "${BLUE}========================================================================${NOCOLOR}"
    echo -e ""
    echo -e "${GREEN}Next Steps:${NOCOLOR}"
    echo -e "  • Verify installation: ${CYAN}curl http://localhost:6001/health${NOCOLOR}"
    echo -e "  • Check services:      ${CYAN}sudo network-cli service status all${NOCOLOR}"
    echo -e "  • View logs:           ${CYAN}sudo network-cli service logs node --follow${NOCOLOR}"
    echo -e "  • Authenticate:        ${CYAN}network-cli auth login${NOCOLOR}"
    echo -e ""
    echo -e "${CYAN}Environment Management:${NOCOLOR}"
    echo -e "  • Switch to devnet:    ${CYAN}network-cli devnet enable${NOCOLOR}"
    echo -e "  • Switch to testnet:   ${CYAN}network-cli testnet enable${NOCOLOR}"
    echo -e "  • Show environment:    ${CYAN}network-cli env current${NOCOLOR}"
    echo -e ""
    echo -e "${CYAN}Documentation: https://docs.debros.io${NOCOLOR}"
    echo -e ""
}

main() {
    display_banner
    
    echo -e ""
    log "Starting DeBros Network installation..."
    echo -e ""
    
    detect_os
    check_architecture
    check_dependencies
    
    echo -e ""
    echo -e "${BLUE}========================================${NOCOLOR}"
    echo -e "${GREEN}Step 1: Install network-cli${NOCOLOR}"
    echo -e "${BLUE}========================================${NOCOLOR}"
    echo -e ""
    
    get_latest_release
    download_and_install
    
    # Verify installation
    if ! verify_installation; then
        exit 1
    fi
    
    # Run setup
    run_setup
    
    # Show completion message
    show_completion
}

main "$@"
