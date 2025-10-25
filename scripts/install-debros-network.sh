#!/bin/bash

# DeBros Network Production Installation Script
# Installs and configures a complete DeBros network node (bootstrap) with gateway.
# Supports idempotent updates and secure systemd service management.

set -e
trap 'echo -e "${RED}An error occurred. Installation aborted.${NOCOLOR}"; exit 1' ERR

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BLUE='\033[38;2;2;128;175m'
YELLOW='\033[1;33m'
NOCOLOR='\033[0m'

# REQUIRE INTERACTIVE MODE - This script must be run in a terminal
if [ ! -t 0 ]; then
    echo -e "${RED}[ERROR]${NOCOLOR} This script requires an interactive terminal."
    echo -e ""
    echo -e "${YELLOW}Please run this script directly in a terminal:${NOCOLOR}"
    echo -e "${CYAN}  sudo bash scripts/install-debros-network.sh${NOCOLOR}"
    echo -e ""
    echo -e "${YELLOW}Do NOT pipe this script:${NOCOLOR}"
    echo -e "${RED}  curl ... | bash${NOCOLOR}  ← This will NOT work"
    echo -e ""
    exit 1
fi

# Get absolute path of this script
SCRIPT_PATH="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"

# Defaults
INSTALL_DIR="/home/debros"
REPO_URL="https://github.com/DeBrosOfficial/network.git"
MIN_GO_VERSION="1.21"
NODE_PORT="4001"
RQLITE_PORT="5001"
GATEWAY_PORT="6001"
RAFT_PORT="7001"
UPDATE_MODE=false
DEBROS_USER="debros"

log() { echo -e "${CYAN}[$(date '+%Y-%m-%d %H:%M:%S')]${NOCOLOR} $1"; }
error() { echo -e "${RED}[ERROR]${NOCOLOR} $1"; }
success() { echo -e "${GREEN}[SUCCESS]${NOCOLOR} $1"; }
warning() { echo -e "${YELLOW}[WARNING]${NOCOLOR} $1"; }

# Check if we need to create debros user first (requires root)
check_and_setup_debros_user() {
    CURRENT_USER=$(whoami)
    
    # If running as debros user directly, we're good to go
    if [ "$CURRENT_USER" = "$DEBROS_USER" ]; then
        return 0
    fi
    
    # If running as root via sudo from debros user, that's also okay for proceeding with installation
    if [ "$CURRENT_USER" = "root" ] && [ "$SUDO_USER" = "$DEBROS_USER" ]; then
        # Switch back to debros user to run the installation properly
        exec sudo -u "$DEBROS_USER" bash "$SCRIPT_PATH"
    fi
    
    # If not debros user and not root, abort and give instructions
    if [ "$CURRENT_USER" != "root" ]; then
        error "This script must be run as root"
        echo -e ""
        echo -e "${YELLOW}To install DeBros Network, run:${NOCOLOR}"
        echo -e "${CYAN}  sudo bash $0${NOCOLOR}"
        echo -e ""
        exit 1
    fi
    
    # At this point, we're running as root but not as debros user
    # Check if debros user exists
    if ! id "$DEBROS_USER" &>/dev/null; then
        log "The '$DEBROS_USER' user does not exist on this system."
        echo -e ""
        echo -e "${YELLOW}DeBros requires a '$DEBROS_USER' system user to run services.${NOCOLOR}"
        echo -e ""
        
        # Ask for permission to create the user
        while true; do
            read -rp "Would you like to create the '$DEBROS_USER' user? (yes/no): " CREATE_USER_CHOICE
            case "$CREATE_USER_CHOICE" in
                [Yy][Ee][Ss]|[Yy])
                    log "Creating system user '$DEBROS_USER'..."
                    useradd -r -m -s /bin/bash -d "$INSTALL_DIR" "$DEBROS_USER" 2>/dev/null || true
                    if id "$DEBROS_USER" &>/dev/null; then
                        success "System user '$DEBROS_USER' created"
                        
                        # Prompt for password
                        echo -e ""
                        log "Setting password for '$DEBROS_USER' user..."
                        echo -e "${YELLOW}Note: You can leave the password empty for passwordless login${NOCOLOR}"
                        echo -e ""
                        echo -n "Enter password for '$DEBROS_USER' user (or press Enter for no password): "
                        read -s DEBROS_PASSWORD
                        echo ""
                        echo -n "Confirm password: "
                        read -s DEBROS_PASSWORD_CONFIRM
                        echo ""
                        
                        # Verify passwords match
                        if [ "$DEBROS_PASSWORD" != "$DEBROS_PASSWORD_CONFIRM" ]; then
                            error "Passwords do not match!"
                            exit 1
                        fi
                        
                        # Set password or enable passwordless login
                        if [ -z "$DEBROS_PASSWORD" ]; then
                            # For passwordless login, we need to use a special approach
                            # First, set a temporary password, then remove it
                            TEMP_PASS="temp123"
                            echo "$DEBROS_USER:$TEMP_PASS" | chpasswd
                            # Now remove the password to make it passwordless
                            passwd -d "$DEBROS_USER" 2>/dev/null || true
                            success "Passwordless login enabled for '$DEBROS_USER' user"
                        else
                            # Set password using chpasswd
                            echo "$DEBROS_USER:$DEBROS_PASSWORD" | chpasswd
                            if [ $? -eq 0 ]; then
                                success "Password set successfully for '$DEBROS_USER' user"
                            else
                                error "Failed to set password for '$DEBROS_USER' user"
                                exit 1
                            fi
                        fi
                    else
                        error "Failed to create user '$DEBROS_USER'"
                        exit 1
                    fi
                    break
                    ;;
                [Nn][Oo]|[Nn])
                    error "Cannot continue without '$DEBROS_USER' user. Exiting."
                    exit 1
                    ;;
                *)
                    error "Invalid choice. Please enter 'yes' or 'no'."
                    ;;
            esac
        done
    else
        log "User '$DEBROS_USER' already exists"
    fi
    
    # Add debros user to sudoers
    log "Adding '$DEBROS_USER' to sudoers..."
    echo "$DEBROS_USER ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/$DEBROS_USER > /dev/null
    sudo chmod 0440 /etc/sudoers.d/$DEBROS_USER
    success "Added '$DEBROS_USER' to sudoers"
    
    # Inform user they need to manually switch to debros user
    echo -e ""
    echo -e "${BLUE}========================================================================${NOCOLOR}"
    echo -e "${YELLOW}IMPORTANT: Manual User Switch Required${NOCOLOR}"
    echo -e "${BLUE}========================================================================${NOCOLOR}"
    echo -e ""
    echo -e "${CYAN}The '$DEBROS_USER' user is now ready.${NOCOLOR}"
    echo -e "${CYAN}To continue the installation, please switch to the '$DEBROS_USER' user${NOCOLOR}"
    echo -e "${CYAN}and re-run the script.${NOCOLOR}"
    echo -e ""
    echo -e "${GREEN}Run the following command:${NOCOLOR}"
    echo -e "${YELLOW}  su - $DEBROS_USER${NOCOLOR}"
    echo -e ""
    echo -e "${CYAN}Then re-run the script with:${NOCOLOR}"
    echo -e "${YELLOW}  bash $SCRIPT_PATH${NOCOLOR}"
    echo -e ""
    echo -e "${BLUE}========================================================================${NOCOLOR}"
    echo -e ""
    exit 0
}

# Check and setup debros user (called before other checks)
check_and_setup_debros_user

# Root/sudo checks
if [[ $EUID -eq 0 ]]; then
    warning "Running as root is not recommended for security reasons."
    echo -n "Are you sure you want to continue? (yes/no): "
    read -r ROOT_CONFIRM
    if [[ "$ROOT_CONFIRM" != "yes" ]]; then
        error "Installation cancelled for security reasons."
        exit 1
    fi
    alias sudo=''
else
    if ! command -v sudo &>/dev/null; then
        error "sudo command not found. Please ensure you have sudo privileges."
        exit 1
    fi
fi

# Detect OS and package manager
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
        *) error "Unsupported operating system: $OS"; exit 1 ;;
    esac
    log "Detected OS: $OS $VERSION"
}

# Check for existing install
check_existing_installation() {
    if [ -d "$INSTALL_DIR" ] && [ -f "$INSTALL_DIR/bin/node" ]; then
        log "Found existing DeBros Network installation at $INSTALL_DIR"
        NODE_RUNNING=false
        if systemctl is-active --quiet debros-node.service 2>/dev/null; then
            NODE_RUNNING=true
            log "Node service is currently running"
        fi
        echo -e "${YELLOW}Existing installation detected!${NOCOLOR}"
        
        # Check if we're running as debros user
        CURRENT_USER=$(whoami)
        if [ "$CURRENT_USER" = "$DEBROS_USER" ]; then
            warning "Running as '$DEBROS_USER' user - only update mode is available"
            echo -e "${CYAN}Options:${NOCOLOR}"
            echo -e "${CYAN}1) Update existing installation${NOCOLOR}"
            echo -e "${CYAN}2) Exit installer${NOCOLOR}"
            while true; do
                read -rp "Enter your choice (1 or 2): " EXISTING_CHOICE
                case $EXISTING_CHOICE in
                    1) UPDATE_MODE=true; log "Will update existing installation"; return 0 ;;
                    2) log "Installation cancelled by user"; exit 0 ;;
                    *) error "Invalid choice. Please enter 1 or 2." ;;
                esac
            done
        else
            echo -e "${CYAN}Options:${NOCOLOR}"
            echo -e "${CYAN}1) Update existing installation${NOCOLOR}"
            echo -e "${CYAN}2) Remove and reinstall${NOCOLOR}"
            echo -e "${CYAN}3) Exit installer${NOCOLOR}"
            while true; do
                read -rp "Enter your choice (1, 2, or 3): " EXISTING_CHOICE
                case $EXISTING_CHOICE in
                    1) UPDATE_MODE=true; log "Will update existing installation"; return 0 ;;
                    2) log "Will remove and reinstall"; remove_existing_installation; UPDATE_MODE=false; return 0 ;;
                    3) log "Installation cancelled by user"; exit 0 ;;
                    *) error "Invalid choice. Please enter 1, 2, or 3." ;;
                esac
            done
        fi
    else
        UPDATE_MODE=false
        return 0
    fi
}

remove_existing_installation() {
    log "Removing existing installation..."
    for service in debros-node debros-gateway; do
        if systemctl list-unit-files | grep -q "$service.service"; then
            log "Stopping $service service..."
            sudo systemctl stop $service.service 2>/dev/null || true
            sudo systemctl disable $service.service 2>/dev/null || true
            sudo rm -f /etc/systemd/system/$service.service
        fi
    done
    sudo systemctl daemon-reload
    if [ -d "$INSTALL_DIR" ]; then
        sudo rm -rf "$INSTALL_DIR"
        log "Removed installation directory"
    fi
    if id "$DEBROS_USER" &>/dev/null; then
        sudo userdel "$DEBROS_USER" 2>/dev/null || true
        log "Removed debros user"
    fi
    success "Existing installation removed"
}

check_go_installation() {
    if command -v go &> /dev/null; then
        GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
        log "Found Go version: $GO_VERSION"
        if [ "$(printf '%s\n' "$MIN_GO_VERSION" "$GO_VERSION" | sort -V | head -n1)" = "$MIN_GO_VERSION" ]; then
            success "Go version is sufficient"
            return 0
        else
            warning "Go version $GO_VERSION is too old. Minimum required: $MIN_GO_VERSION"
            return 1
        fi
    else
        log "Go not found on system"
        return 1
    fi
}

install_go() {
    log "Installing Go..."
    case $PACKAGE_MANAGER in
        apt) sudo apt update; sudo apt install -y wget ;;
        yum|dnf) sudo $PACKAGE_MANAGER install -y wget ;;
    esac
    GO_TARBALL="go1.21.6.linux-amd64.tar.gz"
    ARCH=$(uname -m)
    if [ "$ARCH" = "aarch64" ]; then GO_TARBALL="go1.21.6.linux-arm64.tar.gz"; fi
    cd /tmp
    wget -q "https://go.dev/dl/$GO_TARBALL"
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf "$GO_TARBALL"
    if ! grep -q "/usr/local/go/bin" /etc/environment 2>/dev/null; then
        echo 'PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/games:/usr/local/games:/usr/local/go/bin"' | sudo tee /etc/environment > /dev/null
    fi
    if ! grep -q "/usr/local/go/bin" ~/.bashrc; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
    fi
    export PATH=$PATH:/usr/local/go/bin
    success "Go installed successfully"
}

install_dependencies() {
    log "Checking system dependencies..."
    MISSING_DEPS=()
    case $PACKAGE_MANAGER in
        apt)
            for pkg in git make build-essential curl; do
                if ! dpkg -l | grep -q "^ii  $pkg "; then MISSING_DEPS+=($pkg); fi
            done
            if [ ${#MISSING_DEPS[@]} -gt 0 ]; then
                log "Installing missing dependencies: ${MISSING_DEPS[*]}"
                sudo apt update
                sudo apt install -y "${MISSING_DEPS[@]}"
            else
                success "All system dependencies already installed"
            fi
            ;;
        yum|dnf)
            for pkg in git make curl; do
                if ! rpm -q $pkg &>/dev/null; then MISSING_DEPS+=($pkg); fi
            done
            if ! rpm -q gcc &>/dev/null; then MISSING_DEPS+=("Development Tools"); fi
            if [ ${#MISSING_DEPS[@]} -gt 0 ]; then
                log "Installing missing dependencies: ${MISSING_DEPS[*]}"
                if [[ " ${MISSING_DEPS[*]} " =~ " Development Tools " ]]; then
                    sudo $PACKAGE_MANAGER groupinstall -y "Development Tools"
                fi
                MISSING_DEPS=($(printf '%s\n' "${MISSING_DEPS[@]}" | grep -v "Development Tools"))
                if [ ${#MISSING_DEPS[@]} -gt 0 ]; then
                    sudo $PACKAGE_MANAGER install -y "${MISSING_DEPS[@]}"
                fi
            else
                success "All system dependencies already installed"
            fi
            ;;
    esac
    success "System dependencies ready"
}

install_rqlite() {
    if command -v rqlited &> /dev/null; then
        RQLITE_VERSION=$(rqlited -version | head -n1 | awk '{print $2}')
        log "Found RQLite version: $RQLITE_VERSION"
        success "RQLite already installed"
        return 0
    fi
    log "Installing RQLite..."
    ARCH=$(uname -m)
    case $ARCH in
        x86_64) RQLITE_ARCH="amd64" ;;
        aarch64|arm64) RQLITE_ARCH="arm64" ;;
        armv7l) RQLITE_ARCH="arm" ;;
        *) error "Unsupported architecture: $ARCH"; exit 1 ;;
    esac
    RQLITE_VERSION="8.43.0"
    RQLITE_TARBALL="rqlite-v${RQLITE_VERSION}-linux-${RQLITE_ARCH}.tar.gz"
    RQLITE_URL="https://github.com/rqlite/rqlite/releases/download/v${RQLITE_VERSION}/${RQLITE_TARBALL}"
    cd /tmp
    if ! wget -q "$RQLITE_URL"; then error "Failed to download RQLite from $RQLITE_URL"; exit 1; fi
    tar -xzf "$RQLITE_TARBALL"
    RQLITE_DIR="rqlite-v${RQLITE_VERSION}-linux-${RQLITE_ARCH}"
    sudo cp "$RQLITE_DIR/rqlited" /usr/local/bin/
    sudo cp "$RQLITE_DIR/rqlite" /usr/local/bin/
    sudo chmod +x /usr/local/bin/rqlited
    sudo chmod +x /usr/local/bin/rqlite
    rm -rf "$RQLITE_TARBALL" "$RQLITE_DIR"
    if command -v rqlited &> /dev/null; then
        INSTALLED_VERSION=$(rqlited -version | head -n1 | awk '{print $2}')
        success "RQLite v$INSTALLED_VERSION installed successfully"
    else
        error "RQLite installation failed"
        exit 1
    fi
}

check_ports() {
    local ports=($NODE_PORT $RQLITE_PORT $RAFT_PORT $GATEWAY_PORT)
    for port in "${ports[@]}"; do
        if sudo netstat -tuln 2>/dev/null | grep -q ":$port " || ss -tuln 2>/dev/null | grep -q ":$port "; then
            error "Port $port is already in use. Please free it up and try again."
            exit 1
        fi
    done
    success "All required ports are available"
}

setup_directories() {
    log "Setting up directories and permissions..."
    
    # At this point, debros user should already exist
    # (either we're running as debros, or root just created it)
    if ! id "$DEBROS_USER" &>/dev/null; then
        error "User '$DEBROS_USER' does not exist. This should not happen."
        exit 1
    fi
    
    sudo mkdir -p "$INSTALL_DIR"/{bin,src}
    sudo chown -R "$DEBROS_USER:$DEBROS_USER" "$INSTALL_DIR"
    sudo chmod 755 "$INSTALL_DIR"
    sudo chmod 755 "$INSTALL_DIR/bin"
    
    # Create ~/.debros for the debros user
    DEBROS_HOME=$(sudo -u "$DEBROS_USER" sh -c 'echo ~')
    sudo -u "$DEBROS_USER" mkdir -p "$DEBROS_HOME/.debros"
    sudo chmod 0700 "$DEBROS_HOME/.debros"
    
    success "Directory structure ready"
}

setup_source_code() {
    log "Setting up source code..."
    if [ -d "$INSTALL_DIR/src/.git" ]; then
        log "Updating existing repository (on 'nightly' branch)..."
        cd "$INSTALL_DIR/src"
        # Always ensure we're on 'nightly' before pulling
        sudo -u "$DEBROS_USER" git fetch
        sudo -u "$DEBROS_USER" git checkout nightly || sudo -u "$DEBROS_USER" git checkout -b nightly origin/nightly
        sudo -u "$DEBROS_USER" git pull origin nightly
    else
        log "Cloning repository (branch: nightly)..."
        sudo -u "$DEBROS_USER" git clone --branch nightly --single-branch "$REPO_URL" "$INSTALL_DIR/src"
        cd "$INSTALL_DIR/src"
    fi
    success "Source code ready"
}


build_binaries() {
    log "Building DeBros Network binaries..."
    cd "$INSTALL_DIR/src"
    export PATH=$PATH:/usr/local/go/bin
    
    local services_were_running=()
    if [ "$UPDATE_MODE" = true ]; then
        log "Update mode: checking for running services before binary update..."
        if systemctl is-active --quiet debros-node.service 2>/dev/null; then
            log "Stopping debros-node service to update binaries..."
            sudo systemctl stop debros-node.service
            services_were_running+=("debros-node")
        fi
        if systemctl is-active --quiet debros-gateway.service 2>/dev/null; then
            log "Stopping debros-gateway service to update binaries..."
            sudo systemctl stop debros-gateway.service
            services_were_running+=("debros-gateway")
        fi
        if [ ${#services_were_running[@]} -gt 0 ]; then
            log "Waiting for services to stop completely..."
            sleep 3
        fi
    fi
    
    sudo -u "$DEBROS_USER" env "PATH=$PATH:/usr/local/go/bin" make build
    sudo cp bin/* "$INSTALL_DIR/bin/"
    sudo chown "$DEBROS_USER:$DEBROS_USER" "$INSTALL_DIR/bin/"*
    sudo chmod 755 "$INSTALL_DIR/bin/"*
    
    if [ "$UPDATE_MODE" = true ] && [ ${#services_were_running[@]} -gt 0 ]; then
        log "Restarting previously running services..."
        for service in "${services_were_running[@]}"; do
            log "Starting $service service..."
            sudo systemctl start $service.service
        done
    fi
    success "Binaries built and installed"
}

generate_configs() {
    log "Generating configuration files via network-cli..."
    DEBROS_HOME=$(sudo -u "$DEBROS_USER" sh -c 'echo ~')
    
    # Generate bootstrap config
    log "Generating bootstrap.yaml..."
    sudo -u "$DEBROS_USER" "$INSTALL_DIR/bin/network-cli" config init --type bootstrap --force
    
    # Generate gateway config
    log "Generating gateway.yaml..."
    sudo -u "$DEBROS_USER" "$INSTALL_DIR/bin/network-cli" config init --type gateway --force
    
    success "Configuration files generated"
}

configure_firewall() {
    log "Configuring firewall rules..."
    if command -v ufw &> /dev/null; then
        log "Adding UFW rules for DeBros Network ports..."
        for port in $NODE_PORT $RQLITE_PORT $RAFT_PORT $GATEWAY_PORT; do
            if ! sudo ufw allow $port 2>/dev/null; then
                error "Failed to allow port $port"
                exit 1
            fi
            log "Added UFW rule: allow port $port"
        done
        UFW_STATUS=$(sudo ufw status | grep -o "Status: [a-z]\+" | awk '{print $2}' || echo "inactive")
        if [[ "$UFW_STATUS" == "active" ]]; then
            success "Firewall rules added and active"
        else
            success "Firewall rules added (UFW is inactive - rules will take effect when UFW is enabled)"
            log "To enable UFW with current rules: sudo ufw enable"
        fi
    else
        warning "UFW not found. Please configure firewall manually."
        log "Required ports to allow:"
        log "  - Port $NODE_PORT (Node P2P)"
        log "  - Port $RQLITE_PORT (RQLite HTTP)"
        log "  - Port $RAFT_PORT (RQLite Raft)"
        log "  - Port $GATEWAY_PORT (Gateway)"
    fi
}

create_systemd_services() {
    log "Creating systemd service units..."
    
    # Node service
    local node_service_file="/etc/systemd/system/debros-node.service"
    if [ -f "$node_service_file" ]; then
        log "Cleaning up existing node service..."
        sudo systemctl stop debros-node.service 2>/dev/null || true
        sudo systemctl disable debros-node.service 2>/dev/null || true
        sudo rm -f "$node_service_file"
    fi
    sudo systemctl daemon-reload
    log "Creating new systemd service..."
    local exec_start="$INSTALL_DIR/bin/node --config $INSTALL_DIR/configs/node.yaml"
    cat > /tmp/debros-node.service << EOF
[Unit]
Description=DeBros Network Node (Bootstrap)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=$INSTALL_DIR/src
ExecStart=$INSTALL_DIR/bin/node --config bootstrap.yaml
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=debros-node

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=$INSTALL_DIR

[Install]
WantedBy=multi-user.target
EOF
    sudo mv /tmp/debros-node.service "$node_service_file"
    
    # Gateway service
    local gateway_service_file="/etc/systemd/system/debros-gateway.service"
    if [ -f "$gateway_service_file" ]; then
        log "Cleaning up existing gateway service..."
        sudo systemctl stop debros-gateway.service 2>/dev/null || true
        sudo systemctl disable debros-gateway.service 2>/dev/null || true
        sudo rm -f "$gateway_service_file"
    fi
    
    log "Creating debros-gateway.service..."
    cat > /tmp/debros-gateway.service << EOF
[Unit]
Description=DeBros Gateway (HTTP/WebSocket)
After=debros-node.service
Wants=debros-node.service

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=$INSTALL_DIR/src
ExecStart=$INSTALL_DIR/bin/gateway
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=debros-gateway

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=$INSTALL_DIR

[Install]
WantedBy=multi-user.target
EOF
    sudo mv /tmp/debros-gateway.service "$gateway_service_file"
    
    sudo systemctl daemon-reload
    sudo systemctl enable debros-node.service
    sudo systemctl enable debros-gateway.service
    success "Systemd services ready"
}

start_services() {
    log "Starting DeBros Network services..."
    sudo systemctl start debros-node.service
    sleep 3
    if systemctl is-active --quiet debros-node.service; then
        success "DeBros Node service started successfully"
    else
        error "Failed to start DeBros Node service"
        log "Check logs with: sudo journalctl -u debros-node.service -f"
        exit 1
    fi
    
    sleep 2
    sudo systemctl start debros-gateway.service
    sleep 2
    if systemctl is-active --quiet debros-gateway.service; then
        success "DeBros Gateway service started successfully"
    else
        error "Failed to start DeBros Gateway service"
        log "Check logs with: sudo journalctl -u debros-gateway.service -f"
        exit 1
    fi
}

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
}

main() {
    display_banner
    log "${BLUE}==================================================${NOCOLOR}"
    log "${GREEN}        Starting DeBros Network Installation      ${NOCOLOR}"
    log "${BLUE}==================================================${NOCOLOR}"
    detect_os
    check_existing_installation
    if [ "$UPDATE_MODE" != true ]; then check_ports; else log "Update mode: skipping port availability check"; fi
    if ! check_go_installation; then install_go; fi
    install_dependencies
    install_rqlite
    setup_directories
    setup_source_code
    build_binaries
    if [ "$UPDATE_MODE" != true ]; then
        generate_configs
        configure_firewall
    else
        log "Update mode: keeping existing configuration"
        # But check if configs are missing and generate them if needed
        DEBROS_HOME=$(sudo -u "$DEBROS_USER" sh -c 'echo ~')
        if [ ! -f "$DEBROS_HOME/.debros/bootstrap.yaml" ] || [ ! -f "$DEBROS_HOME/.debros/gateway.yaml" ]; then
            log "Update mode: detected missing configuration files, generating them..."
            generate_configs
        fi
    fi
    create_systemd_services
    start_services
    
    DEBROS_HOME=$(sudo -u "$DEBROS_USER" sh -c 'echo ~')
    
    log "${BLUE}==================================================${NOCOLOR}"
    if [ "$UPDATE_MODE" = true ]; then
        log "${GREEN}        Update Complete!                          ${NOCOLOR}"
    else
        log "${GREEN}        Installation Complete!                    ${NOCOLOR}"
    fi
    log "${BLUE}==================================================${NOCOLOR}"
    log "${GREEN}Installation Directory:${NOCOLOR} ${CYAN}$INSTALL_DIR${NOCOLOR}"
    log "${GREEN}Config Directory:${NOCOLOR} ${CYAN}$DEBROS_HOME/.debros${NOCOLOR}"
    log "${GREEN}LibP2P Port:${NOCOLOR} ${CYAN}$NODE_PORT${NOCOLOR}"
    log "${GREEN}RQLite Port:${NOCOLOR} ${CYAN}$RQLITE_PORT${NOCOLOR}"
    log "${GREEN}Gateway Port (Dev):${NOCOLOR} ${CYAN}$GATEWAY_PORT${NOCOLOR}"
    log "${GREEN}Raft Port:${NOCOLOR} ${CYAN}$RAFT_PORT${NOCOLOR}"
    log "${BLUE}==================================================${NOCOLOR}"
    log "${GREEN}Service Management:${NOCOLOR}"
    log "${CYAN}  - sudo systemctl status debros-node${NOCOLOR} (Check node status)"
    log "${CYAN}  - sudo systemctl status debros-gateway${NOCOLOR} (Check gateway status)"
    log "${CYAN}  - sudo systemctl restart debros-node${NOCOLOR} (Restart node)"
    log "${CYAN}  - sudo systemctl restart debros-gateway${NOCOLOR} (Restart gateway)"
    log "${CYAN}  - sudo systemctl stop debros-node${NOCOLOR} (Stop node)"
    log "${CYAN}  - sudo systemctl stop debros-gateway${NOCOLOR} (Stop gateway)"
    log "${CYAN}  - sudo journalctl -u debros-node.service -f${NOCOLOR} (View node logs)"
    log "${CYAN}  - sudo journalctl -u debros-gateway.service -f${NOCOLOR} (View gateway logs)"
    log "${BLUE}==================================================${NOCOLOR}"
    log "${GREEN}Verify Installation:${NOCOLOR}"
    log "${CYAN}  - Node health: curl http://127.0.0.1:5001/status${NOCOLOR}"
    log "${CYAN}  - Gateway health: curl http://127.0.0.1:6001/health${NOCOLOR}"
    log "${CYAN}  - Show bootstrap peer: cat $DEBROS_HOME/.debros/bootstrap/peer.info${NOCOLOR}"
    log "${BLUE}==================================================${NOCOLOR}"
    
    if [ "$UPDATE_MODE" = true ]; then
        success "DeBros Network has been updated and is running!"
    else
        success "DeBros Network is now running!"
    fi
    log "${CYAN}For documentation visit: https://docs.debros.io${NOCOLOR}"
}

main "$@"
