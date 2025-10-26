#!/bin/bash

# DeBros Network Installation Script (APT-Ready)
# This script installs network-cli and runs the interactive setup command.
# 
# Usage:
#   curl -fsSL https://install.debros.network | bash
#   OR
#   wget -qO- https://install.debros.network | bash
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
APT_REPO_URL="https://debros-official.github.io/network/apt"
APT_KEY_URL="${APT_REPO_URL}/pubkey.gpg"
FALLBACK_REPO="https://github.com/DeBrosOfficial/network.git"
FALLBACK_BRANCH="nightly"

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
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$ID
        VERSION=$VERSION_ID
    else
        error "Cannot detect operating system"
        exit 1
    fi
    
    case $OS in
        ubuntu|debian) PACKAGE_MANAGER="apt" ;;
        centos|rhel|fedora)
            PACKAGE_MANAGER="yum"
            if command -v dnf &> /dev/null; then PACKAGE_MANAGER="dnf"; fi
            ;;
        *) 
            warning "Unsupported operating system: $OS"
            echo -e "${YELLOW}This script is optimized for Ubuntu/Debian.${NOCOLOR}"
            echo -e "${YELLOW}Installation will continue but may require manual steps.${NOCOLOR}"
            PACKAGE_MANAGER="apt"
            ;;
    esac
    
    log "Detected OS: $OS ${VERSION:-unknown}"
}

check_architecture() {
    ARCH=$(uname -m)
    case $ARCH in
        x86_64) ARCH_SUPPORTED=true ;;
        aarch64|arm64) ARCH_SUPPORTED=true ;;
        *) 
            error "Unsupported architecture: $ARCH"
            echo -e "${YELLOW}Supported: x86_64, aarch64/arm64${NOCOLOR}"
            exit 1
            ;;
    esac
    log "Architecture: $ARCH"
}

try_install_from_apt() {
    log "Checking for APT repository..."
    
    # Check if APT repo is available
    if curl -fsSL "$APT_KEY_URL" > /dev/null 2>&1; then
        log "APT repository found! Installing from APT..."
        
        # Download and add GPG key
        curl -fsSL "$APT_KEY_URL" | sudo gpg --dearmor -o /usr/share/keyrings/debros-archive-keyring.gpg
        
        # Add APT source
        echo "deb [signed-by=/usr/share/keyrings/debros-archive-keyring.gpg arch=$(dpkg --print-architecture)] $APT_REPO_URL stable main" | \
            sudo tee /etc/apt/sources.list.d/debros.list > /dev/null
        
        # Update and install
        sudo apt update
        sudo apt install -y debros-network-cli
        
        success "network-cli installed from APT"
        return 0
    else
        warning "APT repository not available yet (coming soon!)"
        return 1
    fi
}

install_from_source() {
    log "Installing network-cli from source..."
    
    # Install build dependencies
    log "Installing build dependencies..."
    case $PACKAGE_MANAGER in
        apt)
            sudo apt update
            sudo apt install -y git make curl wget build-essential
            ;;
        yum|dnf)
            sudo $PACKAGE_MANAGER install -y git make curl wget gcc
            ;;
    esac
    
    # Check/Install Go
    if ! command -v go &> /dev/null; then
        log "Installing Go 1.21.6..."
        GO_ARCH="amd64"
        if [[ "$ARCH" == "aarch64" || "$ARCH" == "arm64" ]]; then
            GO_ARCH="arm64"
        fi
        GO_TARBALL="go1.21.6.linux-${GO_ARCH}.tar.gz"
        
        cd /tmp
        wget -q "https://go.dev/dl/$GO_TARBALL"
        sudo rm -rf /usr/local/go
        sudo tar -C /usr/local -xzf "$GO_TARBALL"
        export PATH=$PATH:/usr/local/go/bin
        
        # Add to PATH permanently
        if ! grep -q "/usr/local/go/bin" ~/.bashrc 2>/dev/null; then
            echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
        fi
        
        success "Go installed"
    else
        log "Go already installed: $(go version)"
    fi
    
    # Clone repository
    TEMP_DIR="/tmp/debros-network-install-$$"
    log "Cloning DeBros Network repository..."
    git clone --branch "$FALLBACK_BRANCH" --single-branch "$FALLBACK_REPO" "$TEMP_DIR"
    cd "$TEMP_DIR"
    
    # Build network-cli
    log "Building network-cli..."
    export PATH=$PATH:/usr/local/go/bin
    make build
    
    # Install to /usr/local/bin
    sudo cp bin/network-cli /usr/local/bin/
    sudo chmod +x /usr/local/bin/network-cli
    
    # Cleanup
    cd /
    rm -rf "$TEMP_DIR"
    
    success "network-cli installed from source"
}

verify_installation() {
    if command -v network-cli &> /dev/null; then
        INSTALLED_VERSION=$(network-cli version 2>/dev/null || echo "unknown")
        success "network-cli is installed: $INSTALLED_VERSION"
        return 0
    else
        error "network-cli installation failed"
        return 1
    fi
}

main() {
    display_banner
    
    echo -e ""
    log "Starting DeBros Network installation..."
    echo -e ""
    
    detect_os
    check_architecture
    
    echo -e ""
    log "${BLUE}========================================${NOCOLOR}"
    log "${GREEN}Step 1: Install network-cli${NOCOLOR}"
    log "${BLUE}========================================${NOCOLOR}"
    
    # Try APT first, fallback to source
    if ! try_install_from_apt; then
        install_from_source
    fi
    
    # Verify installation
    if ! verify_installation; then
        exit 1
    fi
    
    echo -e ""
    log "${BLUE}========================================${NOCOLOR}"
    log "${GREEN}Step 2: Run Interactive Setup${NOCOLOR}"
    log "${BLUE}========================================${NOCOLOR}"
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
        exit 0
    fi
    
    echo -e ""
    log "Running setup (requires sudo)..."
    sudo network-cli setup
    
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

main "$@"
