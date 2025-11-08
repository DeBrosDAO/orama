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
trap 'error "An error occurred. Installation aborted."; execute_traps; exit 1' ERR

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

# Upgrade detection flags
PREVIOUS_INSTALL=false
SETUP_EXECUTED=false
PREVIOUS_VERSION=""
LATEST_VERSION=""
VERSION_CHANGED=false

# Cleanup handlers (for proper trap stacking)
declare -a CLEANUP_HANDLERS

log() { echo -e "${CYAN}[$(date '+%Y-%m-%d %H:%M:%S')]${NOCOLOR} $1"; }
error() { echo -e "${RED}[ERROR]${NOCOLOR} $1" >&2; }
success() { echo -e "${GREEN}[SUCCESS]${NOCOLOR} $1"; }
warning() { echo -e "${YELLOW}[WARNING]${NOCOLOR} $1" >&2; }

# Stack-based trap cleanup
push_trap() {
    local handler="$1"
    local signal="${2:-EXIT}"
    CLEANUP_HANDLERS+=("$handler")
}

execute_traps() {
    for ((i=${#CLEANUP_HANDLERS[@]}-1; i>=0; i--)); do
        eval "${CLEANUP_HANDLERS[$i]}"
    done
}

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
    
    # Check if jq is available for robust JSON parsing
    if command -v jq &>/dev/null; then
        # Use jq for structured JSON parsing
        LATEST_RELEASE=$(curl -fsSL -H "Accept: application/vnd.github+json" "$GITHUB_API/releases" | \
            jq -r '.[] | select(.prerelease == false and .draft == false) | .tag_name' | head -1)
    else
        # Fallback to grep-based parsing
        log "Note: jq not available, using basic parsing (consider installing jq for robustness)"
        LATEST_RELEASE=$(curl -fsSL "$GITHUB_API/releases" | \
            grep -v "prerelease.*true" | \
            grep -v "draft.*true" | \
            grep '"tag_name"' | \
            head -1 | \
            cut -d'"' -f4)
    fi
    
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
    CHECKSUM_URL="https://github.com/$GITHUB_REPO/releases/download/$LATEST_RELEASE/checksums.txt"
    
    # Create temporary directory
    TEMP_DIR=$(mktemp -d)
    push_trap "rm -rf $TEMP_DIR" EXIT
    
    # Download
    log "Downloading from: $DOWNLOAD_URL"
    if ! curl -fsSL -o "$TEMP_DIR/network-cli.tar.gz" "$DOWNLOAD_URL"; then
        error "Failed to download network-cli"
        exit 1
    fi
    
    # Try to download and verify checksum
    CHECKSUM_FILE="$TEMP_DIR/checksums.txt"
    if curl -fsSL -o "$CHECKSUM_FILE" "$CHECKSUM_URL" 2>/dev/null; then
        log "Verifying checksum..."
        cd "$TEMP_DIR"
        if command -v sha256sum &>/dev/null; then
            if sha256sum -c "$CHECKSUM_FILE" --ignore-missing >/dev/null 2>&1; then
                success "Checksum verified"
            else
                warning "Checksum verification failed (continuing anyway)"
            fi
        else
            log "sha256sum not available, skipping checksum verification"
        fi
    else
        log "Checksums not available for this release (continuing without verification)"
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

check_existing_installation() {
    if command -v network-cli &>/dev/null 2>&1; then
        PREVIOUS_INSTALL=true
        PREVIOUS_VERSION=$(network-cli version 2>/dev/null | head -n1 || echo "unknown")
        echo -e ""
        echo -e "${YELLOW}⚠️  Existing installation detected: ${PREVIOUS_VERSION}${NOCOLOR}"
        echo -e ""
        
        # Version will be compared after fetching latest release
        # If they match, we skip the service stop/restart to minimize downtime
    else
        log "No previous installation detected - performing fresh install"
    fi
}

compare_versions() {
    # Compare previous and latest versions
    if [ "$PREVIOUS_INSTALL" = true ] && [ ! -z "$PREVIOUS_VERSION" ] && [ ! -z "$LATEST_VERSION" ]; then
        if [ "$PREVIOUS_VERSION" = "$LATEST_VERSION" ]; then
            VERSION_CHANGED=false
            log "Installed version ($PREVIOUS_VERSION) matches latest release ($LATEST_VERSION)"
            log "Skipping service restart - no upgrade needed"
            return 0
        else
            VERSION_CHANGED=true
            log "Version change detected: $PREVIOUS_VERSION → $LATEST_VERSION"
            log "Services will be stopped before updating."
            echo -e ""
            
            # Check if services are running
            if sudo network-cli service status all >/dev/null 2>&1; then
                log "Stopping DeBros services before upgrade..."
                log "Note: Anon (if running) will not be stopped as it may be managed separately"
                if sudo network-cli service stop all; then
                    success "DeBros services stopped successfully"
                else
                    warning "Failed to stop some services (continuing anyway)"
                fi
            else
                log "DeBros services already stopped or not running"
            fi
        fi
    fi
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

# Check if port 9050 is in use (Anon SOCKS port)
is_anon_running() {
    # Check if port 9050 is listening
    if command -v ss &>/dev/null; then
        if ss -tlnp 2>/dev/null | grep -q ":9050"; then
            return 0
        fi
    elif command -v netstat &>/dev/null; then
        if netstat -tlnp 2>/dev/null | grep -q ":9050"; then
            return 0
        fi
    elif command -v lsof &>/dev/null; then
        # Try to check without sudo first (in case of passwordless sudo issues)
        if sudo -n lsof -i :9050 >/dev/null 2>&1; then
            return 0
        fi
    fi
    
    # Fallback: assume Anon is not running if we can't determine
    return 1
}

install_anon() {
    echo -e ""
    echo -e "${BLUE}========================================${NOCOLOR}"
    echo -e "${GREEN}Step 1.5: Install Anyone Relay (Anon)${NOCOLOR}"
    echo -e "${BLUE}========================================${NOCOLOR}"
    echo -e ""
    
    log "Checking Anyone relay (Anon) status..."
    
    # Check if Anon is already running on port 9050
    if is_anon_running; then
        success "Anon is already running on port 9050"
        log "Skipping Anon installation - using existing instance"
        configure_anon_logs
        configure_firewall_for_anon
        return 0
    fi
    
    # Check if anon binary is already installed
    if command -v anon &>/dev/null; then
        success "Anon binary already installed"
        log "Anon is installed but not running. You can start it manually if needed."
        configure_anon_logs
        configure_firewall_for_anon
        return 0
    fi
    
    log "Installing Anyone relay for anonymous networking..."
    
    # Install via APT (official method from docs.anyone.io)
    log "Adding Anyone APT repository..."
    
    # Add GPG key
    if ! curl -fsSL https://deb.anyone.io/gpg.key | sudo gpg --dearmor -o /usr/share/keyrings/anyone-archive-keyring.gpg 2>/dev/null; then
        warning "Failed to add Anyone GPG key"
        log "You can manually install later with:"
        log "  curl -fsSL https://deb.anyone.io/gpg.key | sudo gpg --dearmor -o /usr/share/keyrings/anyone-archive-keyring.gpg"
        log "  echo 'deb [signed-by=/usr/share/keyrings/anyone-archive-keyring.gpg] https://deb.anyone.io/ anyone main' | sudo tee /etc/apt/sources.list.d/anyone.list"
        log "  sudo apt update && sudo apt install -y anon"
        return 1
    fi
    
    # Add repository
    echo "deb [signed-by=/usr/share/keyrings/anyone-archive-keyring.gpg] https://deb.anyone.io/ anyone main" | sudo tee /etc/apt/sources.list.d/anyone.list >/dev/null
    
    # Preseed terms acceptance to avoid interactive prompt
    log "Pre-accepting Anon terms and conditions..."
    # Try multiple debconf question formats
    echo "anon anon/terms boolean true" | sudo debconf-set-selections
    echo "anon anon/terms seen true" | sudo debconf-set-selections
    # Also try with select/string format
    echo "anon anon/terms select true" | sudo debconf-set-selections || true
    
    # Query debconf to verify the question exists and set it properly
    # Some packages use different question formats
    sudo debconf-get-selections | grep -i anon || true
    
    # Create anonrc directory and file with AgreeToTerms before installation
    # This ensures terms are accepted even if the post-install script checks the file
    sudo mkdir -p /etc/anon
    if [ ! -f /etc/anon/anonrc ]; then
        echo "AgreeToTerms 1" | sudo tee /etc/anon/anonrc >/dev/null
    fi
    
    # Also create a terms-agreement file if Anon checks for it
    # Check multiple possible locations where Anon might look for terms acceptance
    sudo mkdir -p /var/lib/anon
    echo "agreed" | sudo tee /var/lib/anon/terms-agreement >/dev/null 2>&1 || true
    sudo mkdir -p /usr/share/anon
    echo "agreed" | sudo tee /usr/share/anon/terms-agreement >/dev/null 2>&1 || true
    # Also create near the GPG keyring directory (as the user suggested)
    sudo mkdir -p /usr/share/keyrings/anon
    echo "agreed" | sudo tee /usr/share/keyrings/anon/terms-agreement >/dev/null 2>&1 || true
    # Create in the keyring directory itself as a marker file
    echo "agreed" | sudo tee /usr/share/keyrings/anyone-terms-agreed >/dev/null 2>&1 || true
    
    # Update and install with non-interactive frontend
    log "Installing Anon package..."
    sudo apt update -qq
    
    # Use DEBIAN_FRONTEND=noninteractive and set debconf values directly via apt-get options
    # This is more reliable than just debconf-set-selections
    if ! sudo DEBIAN_FRONTEND=noninteractive \
        apt-get install -y \
        -o Dpkg::Options::="--force-confdef" \
        -o Dpkg::Options::="--force-confold" \
        anon; then
        warning "Anon installation failed"
        return 1
    fi
    
    # Verify installation
    if ! command -v anon &>/dev/null; then
        warning "Anon installation may have failed"
        return 1
    fi
    
    success "Anon installed successfully"
    
    # Configure with sensible defaults
    configure_anon_defaults
    
    # Configure log directory
    configure_anon_logs
    
    # Configure firewall if present
    configure_firewall_for_anon
    
    # Enable and start service
    log "Enabling Anon service..."
    sudo systemctl enable anon 2>/dev/null || true
    sudo systemctl start anon 2>/dev/null || true
    
    if systemctl is-active --quiet anon; then
        success "Anon service is running"
    else
        warning "Anon service may not be running. Check: sudo systemctl status anon"
    fi
    
    return 0
}

configure_anon_defaults() {
    log "Configuring Anon with default settings..."
    
    HOSTNAME=$(hostname -s 2>/dev/null || echo "debros-node")
    
    # Create or update anonrc with our defaults
    if [ -f /etc/anon/anonrc ]; then
        # Backup existing config
        sudo cp /etc/anon/anonrc /etc/anon/anonrc.bak 2>/dev/null || true
        
        # Update key settings if not already set
        if ! grep -q "^Nickname" /etc/anon/anonrc; then
            echo "Nickname ${HOSTNAME}" | sudo tee -a /etc/anon/anonrc >/dev/null
        fi
        
        if ! grep -q "^ControlPort" /etc/anon/anonrc; then
            echo "ControlPort 9051" | sudo tee -a /etc/anon/anonrc >/dev/null
        fi
        
        if ! grep -q "^SocksPort" /etc/anon/anonrc; then
            echo "SocksPort 9050" | sudo tee -a /etc/anon/anonrc >/dev/null
        fi
        
        # Auto-accept terms in config file
        if ! grep -q "^AgreeToTerms" /etc/anon/anonrc; then
            echo "AgreeToTerms 1" | sudo tee -a /etc/anon/anonrc >/dev/null
        fi
        
        log "  Nickname: ${HOSTNAME}"
        log "  ORPort: 9001 (default)"
        log "  ControlPort: 9051"
        log "  SOCKSPort: 9050"
        log "  AgreeToTerms: 1 (auto-accepted)"
    fi
}

configure_anon_logs() {
    log "Configuring Anon logs..."
    
    # Create log directory
    sudo mkdir -p /home/debros/.debros/logs/anon
    
    # Change ownership to debian-anon (the user anon runs as)
    sudo chown -R debian-anon:debian-anon /home/debros/.debros/logs/anon 2>/dev/null || true
    
    # Update anonrc to point logs to our directory
    if [ -f /etc/anon/anonrc ]; then
        sudo sed -i.bak 's|Log notice file.*|Log notice file /home/debros/.debros/logs/anon/notices.log|g' /etc/anon/anonrc
        success "Anon logs configured to /home/debros/.debros/logs/anon"
    fi
}

configure_firewall_for_anon() {
    log "Checking firewall configuration..."
    
    # Check for UFW
    if command -v ufw &>/dev/null && sudo ufw status | grep -q "Status: active"; then
        log "UFW detected and active, adding Anon ports..."
        sudo ufw allow 9001/tcp comment 'Anon ORPort' 2>/dev/null || true
        sudo ufw allow 9051/tcp comment 'Anon ControlPort' 2>/dev/null || true
        success "UFW rules added for Anon"
        return 0
    fi
    
    # Check for firewalld
    if command -v firewall-cmd &>/dev/null && sudo firewall-cmd --state 2>/dev/null | grep -q "running"; then
        log "firewalld detected and active, adding Anon ports..."
        sudo firewall-cmd --permanent --add-port=9001/tcp 2>/dev/null || true
        sudo firewall-cmd --permanent --add-port=9051/tcp 2>/dev/null || true
        sudo firewall-cmd --reload 2>/dev/null || true
        success "firewalld rules added for Anon"
        return 0
    fi
    
    # Check for iptables
    if command -v iptables &>/dev/null; then
        # Check if iptables has any rules (indicating it's in use)
        if sudo iptables -L -n | grep -q "Chain INPUT"; then
            log "iptables detected, adding Anon ports..."
            sudo iptables -A INPUT -p tcp --dport 9001 -j ACCEPT -m comment --comment "Anon ORPort" 2>/dev/null || true
            sudo iptables -A INPUT -p tcp --dport 9051 -j ACCEPT -m comment --comment "Anon ControlPort" 2>/dev/null || true
            
            # Try to save rules if iptables-persistent is available
            if command -v netfilter-persistent &>/dev/null; then
                sudo netfilter-persistent save 2>/dev/null || true
            elif command -v iptables-save &>/dev/null; then
                sudo iptables-save | sudo tee /etc/iptables/rules.v4 >/dev/null 2>&1 || true
            fi
            success "iptables rules added for Anon"
            return 0
        fi
    fi
    
    log "No active firewall detected, skipping firewall configuration"
}

configure_firewall_for_olric() {
    log "Checking firewall configuration for Olric..."
    
    # Check for UFW
    if command -v ufw &>/dev/null && sudo ufw status | grep -q "Status: active"; then
        log "UFW detected and active, adding Olric ports..."
        sudo ufw allow 3320/tcp comment 'Olric HTTP API' 2>/dev/null || true
        sudo ufw allow 3322/tcp comment 'Olric Memberlist' 2>/dev/null || true
        success "UFW rules added for Olric"
        return 0
    fi
    
    # Check for firewalld
    if command -v firewall-cmd &>/dev/null && sudo firewall-cmd --state 2>/dev/null | grep -q "running"; then
        log "firewalld detected and active, adding Olric ports..."
        sudo firewall-cmd --permanent --add-port=3320/tcp 2>/dev/null || true
        sudo firewall-cmd --permanent --add-port=3322/tcp 2>/dev/null || true
        sudo firewall-cmd --reload 2>/dev/null || true
        success "firewalld rules added for Olric"
        return 0
    fi
    
    # Check for iptables
    if command -v iptables &>/dev/null; then
        # Check if iptables has any rules (indicating it's in use)
        if sudo iptables -L -n | grep -q "Chain INPUT"; then
            log "iptables detected, adding Olric ports..."
            sudo iptables -A INPUT -p tcp --dport 3320 -j ACCEPT -m comment --comment "Olric HTTP API" 2>/dev/null || true
            sudo iptables -A INPUT -p tcp --dport 3322 -j ACCEPT -m comment --comment "Olric Memberlist" 2>/dev/null || true
            
            # Try to save rules if iptables-persistent is available
            if command -v netfilter-persistent &>/dev/null; then
                sudo netfilter-persistent save 2>/dev/null || true
            elif command -v iptables-save &>/dev/null; then
                sudo iptables-save | sudo tee /etc/iptables/rules.v4 >/dev/null 2>&1 || true
            fi
            success "iptables rules added for Olric"
            return 0
        fi
    fi
    
    log "No active firewall detected for Olric, skipping firewall configuration"
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
    SETUP_EXECUTED=true
    sudo network-cli setup
}

perform_health_check() {
    echo -e ""
    echo -e "${BLUE}========================================${NOCOLOR}"
    log "Performing post-install health checks..."
    echo -e "${BLUE}========================================${NOCOLOR}"
    echo -e ""
    
    local health_ok=true
    
    # Give services a moment to start if they were just restarted
    sleep 2
    
    # Check gateway health
    if curl -sf http://localhost:6001/health >/dev/null 2>&1; then
        success "Gateway health check passed"
    else
        warning "Gateway health check failed - check logs with: sudo network-cli service logs gateway"
        health_ok=false
    fi
    
    # Check if node is running (may not respond immediately)
    if sudo network-cli service status node >/dev/null 2>&1; then
        success "Node service is running"
    else
        warning "Node service is not running - check with: sudo network-cli service status node"
        health_ok=false
    fi
    
    echo -e ""
    if [ "$health_ok" = true ]; then
        success "All health checks passed!"
    else
        warning "Some health checks failed - review logs and start services if needed"
    fi
    echo -e ""
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
    echo -e "${CYAN}Anyone Relay (Anon):${NOCOLOR}"
    echo -e "  • Check Anon status:   ${CYAN}sudo systemctl status anon${NOCOLOR}"
    echo -e "  • View Anon logs:      ${CYAN}sudo tail -f /home/debros/.debros/logs/anon/notices.log${NOCOLOR}"
    echo -e "  • Proxy endpoint:      ${CYAN}POST http://localhost:6001/v1/proxy/anon${NOCOLOR}"
    echo -e ""
    echo -e "${CYAN}🔐 Shared Secrets (for adding more nodes):${NOCOLOR}"
    echo -e "  • Swarm key:           ${CYAN}cat /home/debros/.debros/swarm.key${NOCOLOR}"
    echo -e "  • Cluster secret:      ${CYAN}sudo cat /home/debros/.debros/cluster-secret${NOCOLOR}"
    echo -e "  • Copy these to bootstrap node before setting up secondary nodes${NOCOLOR}"
    echo -e ""
    echo -e "${CYAN}Documentation: https://docs.debros.io${NOCOLOR}"
    echo -e ""
}

main() {
    display_banner
    
    echo -e ""
    log "Starting DeBros Network installation..."
    echo -e ""
    
    # Check for existing installation and stop services if needed
    check_existing_installation
    
    detect_os
    check_architecture
    check_dependencies
    
    echo -e ""
    echo -e "${BLUE}========================================${NOCOLOR}"
    echo -e "${GREEN}Step 1: Install network-cli${NOCOLOR}"
    echo -e "${BLUE}========================================${NOCOLOR}"
    echo -e ""
    
    get_latest_release
    LATEST_VERSION="$LATEST_RELEASE"
    
    # Compare versions and determine if upgrade is needed
    compare_versions
    
    download_and_install
    
    # Verify installation
    if ! verify_installation; then
        exit 1
    fi
    
    # Install Anon (optional but recommended)
    install_anon || warning "Anon installation skipped or failed"
    
    # Run setup
    run_setup
    
    # If this was an upgrade and setup wasn't run, restart services
    if [ "$PREVIOUS_INSTALL" = true ] && [ "$VERSION_CHANGED" = true ] && [ "$SETUP_EXECUTED" = false ]; then
        echo -e ""
        log "Restarting services that were stopped earlier..."
        
        # Check services individually and provide detailed feedback
        failed_services=()
        if ! sudo network-cli service start all 2>&1 | tee /tmp/service-start.log; then
            # Parse which services failed
            while IFS= read -r line; do
                if [[ $line =~ "Failed to start" ]]; then
                    service_name=$(echo "$line" | grep -oP '(?<=Failed to start\s)\S+(?=:)' || echo "unknown")
                    failed_services+=("$service_name")
                fi
            done < /tmp/service-start.log
            
            if [ ${#failed_services[@]} -gt 0 ]; then
                error "Failed to restart: ${failed_services[*]}"
                error "Please check service status: sudo network-cli service status all"
            fi
        else
            success "Services restarted successfully"
        fi
    fi
    
    # Post-install health check
    perform_health_check
    
    # Show completion message
    show_completion
}

main "$@"
