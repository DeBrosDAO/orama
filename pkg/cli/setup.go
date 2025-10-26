package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// HandleSetupCommand handles the interactive 'setup' command for VPS installation
func HandleSetupCommand(args []string) {
	// Parse flags
	force := false
	for _, arg := range args {
		if arg == "--force" {
			force = true
		}
	}

	fmt.Printf("🚀 DeBros Network Setup\n\n")

	// Check if running as root
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ This command must be run as root (use sudo)\n")
		os.Exit(1)
	}

	// Check OS compatibility
	if runtime.GOOS != "linux" {
		fmt.Fprintf(os.Stderr, "❌ Setup command is only supported on Linux\n")
		fmt.Fprintf(os.Stderr, "   For other platforms, please install manually\n")
		os.Exit(1)
	}

	// Detect OS
	osInfo := detectLinuxDistro()
	fmt.Printf("📋 Detected OS: %s\n", osInfo)

	if !isSupportedOS(osInfo) {
		fmt.Fprintf(os.Stderr, "⚠️  Unsupported OS: %s\n", osInfo)
		fmt.Fprintf(os.Stderr, "   Supported: Ubuntu 22.04/24.04/25.04, Debian 12\n")
		fmt.Printf("\nContinue anyway? (yes/no): ")
		if !promptYesNo() {
			fmt.Println("Setup cancelled.")
			os.Exit(1)
		}
	}

	// Show setup plan
	fmt.Printf("\n" + strings.Repeat("=", 70) + "\n")
	fmt.Printf("Setup Plan:\n")
	fmt.Printf("  1. Create 'debros' system user (if needed)\n")
	fmt.Printf("  2. Install system dependencies (curl, git, make, build tools)\n")
	fmt.Printf("  3. Install Go 1.21+ (if needed)\n")
	fmt.Printf("  4. Install RQLite database\n")
	fmt.Printf("  5. Create directories (/home/debros/bin, /home/debros/src)\n")
	fmt.Printf("  6. Clone and build DeBros Network\n")
	fmt.Printf("  7. Generate configuration files\n")
	fmt.Printf("  8. Create systemd services (debros-node, debros-gateway)\n")
	fmt.Printf("  9. Start and enable services\n")
	fmt.Printf(strings.Repeat("=", 70) + "\n\n")

	fmt.Printf("Ready to begin setup? (yes/no): ")
	if !promptYesNo() {
		fmt.Println("Setup cancelled.")
		os.Exit(1)
	}

	fmt.Printf("\n")

	// Step 1: Setup debros user
	setupDebrosUser()

	// Step 2: Install dependencies
	installSystemDependencies()

	// Step 3: Install Go
	ensureGo()

	// Step 4: Install RQLite
	installRQLite()

	// Step 5: Setup directories
	setupDirectories()

	// Step 6: Clone and build
	cloneAndBuild()

	// Step 7: Generate configs (interactive)
	generateConfigsInteractive(force)

	// Step 8: Create systemd services
	createSystemdServices()

	// Step 9: Start services
	startServices()

	// Done!
	fmt.Printf("\n" + strings.Repeat("=", 70) + "\n")
	fmt.Printf("✅ Setup Complete!\n")
	fmt.Printf(strings.Repeat("=", 70) + "\n\n")
	fmt.Printf("DeBros Network is now running!\n\n")
	fmt.Printf("Service Management:\n")
	fmt.Printf("  network-cli service status all\n")
	fmt.Printf("  network-cli service logs node --follow\n")
	fmt.Printf("  network-cli service restart gateway\n\n")
	fmt.Printf("Verify Installation:\n")
	fmt.Printf("  curl http://localhost:6001/health\n")
	fmt.Printf("  curl http://localhost:5001/status\n\n")
}

func detectLinuxDistro() string {
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		lines := strings.Split(string(data), "\n")
		var id, version string
		for _, line := range lines {
			if strings.HasPrefix(line, "ID=") {
				id = strings.Trim(strings.TrimPrefix(line, "ID="), "\"")
			}
			if strings.HasPrefix(line, "VERSION_ID=") {
				version = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
			}
		}
		if id != "" && version != "" {
			return fmt.Sprintf("%s %s", id, version)
		}
		if id != "" {
			return id
		}
	}
	return "unknown"
}

func isSupportedOS(osInfo string) bool {
	supported := []string{
		"ubuntu 22.04",
		"ubuntu 24.04",
		"ubuntu 25.04",
		"debian 12",
	}
	for _, s := range supported {
		if strings.Contains(strings.ToLower(osInfo), s) {
			return true
		}
	}
	return false
}

func promptYesNo() bool {
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "yes" || response == "y"
}

// isValidMultiaddr validates bootstrap peer multiaddr format
func isValidMultiaddr(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if !(strings.HasPrefix(s, "/ip4/") || strings.HasPrefix(s, "/ip6/")) {
		return false
	}
	return strings.Contains(s, "/p2p/")
}

// isValidHostPort validates host:port format
func isValidHostPort(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return false
	}
	host := strings.TrimSpace(parts[0])
	port := strings.TrimSpace(parts[1])
	if host == "" {
		return false
	}
	// Port must be a valid number between 1 and 65535
	if portNum, err := strconv.Atoi(port); err != nil || portNum < 1 || portNum > 65535 {
		return false
	}
	return true
}

func setupDebrosUser() {
	fmt.Printf("👤 Setting up 'debros' user...\n")

	// Check if user exists
	if _, err := exec.Command("id", "debros").CombinedOutput(); err == nil {
		fmt.Printf("   ✓ User 'debros' already exists\n")
		return
	}

	// Create user
	cmd := exec.Command("useradd", "-r", "-m", "-s", "/bin/bash", "-d", "/home/debros", "debros")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to create user 'debros': %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("   ✓ Created user 'debros'\n")
}

func installSystemDependencies() {
	fmt.Printf("📦 Installing system dependencies...\n")

	// Detect package manager
	var installCmd *exec.Cmd
	if _, err := exec.LookPath("apt"); err == nil {
		installCmd = exec.Command("apt", "update")
		if err := installCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  apt update failed: %v\n", err)
		}
		installCmd = exec.Command("apt", "install", "-y", "curl", "git", "make", "build-essential", "wget")
	} else if _, err := exec.LookPath("yum"); err == nil {
		installCmd = exec.Command("yum", "install", "-y", "curl", "git", "make", "gcc", "wget")
	} else {
		fmt.Fprintf(os.Stderr, "❌ No supported package manager found\n")
		os.Exit(1)
	}

	if err := installCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to install dependencies: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("   ✓ Dependencies installed\n")
}

func ensureGo() {
	fmt.Printf("🔧 Checking Go installation...\n")

	// Check if Go is already installed
	if _, err := exec.LookPath("go"); err == nil {
		fmt.Printf("   ✓ Go already installed\n")
		return
	}

	fmt.Printf("   Installing Go 1.21.6...\n")

	// Download Go
	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	goTarball := fmt.Sprintf("go1.21.6.linux-%s.tar.gz", arch)
	goURL := fmt.Sprintf("https://go.dev/dl/%s", goTarball)

	// Download
	cmd := exec.Command("wget", "-q", goURL, "-O", "/tmp/"+goTarball)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to download Go: %v\n", err)
		os.Exit(1)
	}

	// Extract
	cmd = exec.Command("tar", "-C", "/usr/local", "-xzf", "/tmp/"+goTarball)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to extract Go: %v\n", err)
		os.Exit(1)
	}

	// Add to PATH for current process
	os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/go/bin")

	// Also add to debros user's .bashrc for persistent availability
	debrosHome := "/home/debros"
	bashrc := debrosHome + "/.bashrc"
	pathLine := "\nexport PATH=$PATH:/usr/local/go/bin\n"

	// Read existing bashrc
	existing, _ := os.ReadFile(bashrc)
	existingStr := string(existing)

	// Add PATH if not already present
	if !strings.Contains(existingStr, "/usr/local/go/bin") {
		if err := os.WriteFile(bashrc, []byte(existingStr+pathLine), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to update debros .bashrc: %v\n", err)
		}
		// Fix ownership
		exec.Command("chown", "debros:debros", bashrc).Run()
	}

	fmt.Printf("   ✓ Go installed\n")
}

func installRQLite() {
	fmt.Printf("🗄️  Installing RQLite...\n")

	// Check if already installed
	if _, err := exec.LookPath("rqlited"); err == nil {
		fmt.Printf("   ✓ RQLite already installed\n")
		return
	}

	arch := "amd64"
	switch runtime.GOARCH {
	case "arm64":
		arch = "arm64"
	case "arm":
		arch = "arm"
	}

	version := "8.43.0"
	tarball := fmt.Sprintf("rqlite-v%s-linux-%s.tar.gz", version, arch)
	url := fmt.Sprintf("https://github.com/rqlite/rqlite/releases/download/v%s/%s", version, tarball)

	// Download
	cmd := exec.Command("wget", "-q", url, "-O", "/tmp/"+tarball)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to download RQLite: %v\n", err)
		os.Exit(1)
	}

	// Extract
	cmd = exec.Command("tar", "-C", "/tmp", "-xzf", "/tmp/"+tarball)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to extract RQLite: %v\n", err)
		os.Exit(1)
	}

	// Copy binaries
	dir := fmt.Sprintf("/tmp/rqlite-v%s-linux-%s", version, arch)
	exec.Command("cp", dir+"/rqlited", "/usr/local/bin/").Run()
	exec.Command("cp", dir+"/rqlite", "/usr/local/bin/").Run()
	exec.Command("chmod", "+x", "/usr/local/bin/rqlited").Run()
	exec.Command("chmod", "+x", "/usr/local/bin/rqlite").Run()

	fmt.Printf("   ✓ RQLite installed\n")
}

func setupDirectories() {
	fmt.Printf("📁 Creating directories...\n")

	dirs := []string{
		"/home/debros/bin",
		"/home/debros/src",
		"/home/debros/.debros",
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to create %s: %v\n", dir, err)
			os.Exit(1)
		}
		// Change ownership to debros
		cmd := exec.Command("chown", "-R", "debros:debros", dir)
		cmd.Run()
	}

	fmt.Printf("   ✓ Directories created\n")
}

func cloneAndBuild() {
	fmt.Printf("🔨 Cloning and building DeBros Network...\n")

	// Check if already cloned
	if _, err := os.Stat("/home/debros/src/.git"); err == nil {
		fmt.Printf("   Updating repository...\n")
		cmd := exec.Command("sudo", "-u", "debros", "git", "-C", "/home/debros/src", "pull", "origin", "nightly")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to update repo: %v\n", err)
		}
	} else {
		fmt.Printf("   Cloning repository...\n")
		cmd := exec.Command("sudo", "-u", "debros", "git", "clone", "--branch", "nightly", "--depth", "1", "https://github.com/DeBrosOfficial/network.git", "/home/debros/src")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to clone repo: %v\n", err)
			os.Exit(1)
		}
	}

	// Build
	fmt.Printf("   Building binaries...\n")

	// Ensure Go is in PATH for the build
	os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/go/bin")

	// Use sudo with --preserve-env=PATH to pass Go path to debros user
	cmd := exec.Command("sudo", "--preserve-env=PATH", "-u", "debros", "make", "build")
	cmd.Dir = "/home/debros/src"
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to build: %v\n%s\n", err, output)
		os.Exit(1)
	}

	// Copy binaries
	exec.Command("sh", "-c", "cp -r /home/debros/src/bin/* /home/debros/bin/").Run()
	exec.Command("chown", "-R", "debros:debros", "/home/debros/bin").Run()
	exec.Command("chmod", "-R", "755", "/home/debros/bin").Run()

	fmt.Printf("   ✓ Built and installed\n")
}

func generateConfigsInteractive(force bool) {
	fmt.Printf("⚙️  Generating configurations...\n")

	// For single-node VPS setup, use sensible defaults
	// This creates a bootstrap node that acts as the cluster leader
	fmt.Printf("\n")
	fmt.Printf("Setting up single-node configuration...\n")
	fmt.Printf("  • Bootstrap node (cluster leader)\n")
	fmt.Printf("  • No external peers required\n")
	fmt.Printf("  • Gateway connected to local node\n\n")

	// Generate bootstrap node config with explicit parameters
	// Pass empty bootstrap-peers and no join address for bootstrap node
	bootstrapArgs := []string{
		"-u", "debros",
		"/home/debros/bin/network-cli", "config", "init",
		"--type", "bootstrap",
		"--bootstrap-peers", "",
	}
	if force {
		bootstrapArgs = append(bootstrapArgs, "--force")
	}

	cmd := exec.Command("sudo", bootstrapArgs...)
	cmd.Stdin = nil // Explicitly close stdin to prevent interactive prompts
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to generate bootstrap config: %v\n", err)
		if len(output) > 0 {
			fmt.Fprintf(os.Stderr, "   Output: %s\n", string(output))
		}
	} else {
		fmt.Printf("   ✓ Bootstrap node config created\n")
	}

	// Rename bootstrap.yaml to node.yaml so the service can find it
	renameCmd := exec.Command("sudo", "-u", "debros", "mv", "/home/debros/.debros/bootstrap.yaml", "/home/debros/.debros/node.yaml")
	if err := renameCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to rename config: %v\n", err)
	}

	// Generate gateway config with explicit empty bootstrap peers
	gatewayArgs := []string{
		"-u", "debros",
		"/home/debros/bin/network-cli", "config", "init",
		"--type", "gateway",
		"--bootstrap-peers", "",
	}
	if force {
		gatewayArgs = append(gatewayArgs, "--force")
	}

	cmd = exec.Command("sudo", gatewayArgs...)
	cmd.Stdin = nil // Explicitly close stdin to prevent interactive prompts
	output, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to generate gateway config: %v\n", err)
		if len(output) > 0 {
			fmt.Fprintf(os.Stderr, "   Output: %s\n", string(output))
		}
	} else {
		fmt.Printf("   ✓ Gateway config created\n")
	}

	fmt.Printf("   ✓ Configurations generated\n")
}

func createSystemdServices() {
	fmt.Printf("🔧 Creating systemd services...\n")

	// Node service
	nodeService := `[Unit]
Description=DeBros Network Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=/home/debros/src
ExecStart=/home/debros/bin/node --config node.yaml
Environment=PATH=/usr/local/bin:/usr/bin:/bin
Environment=HOME=/home/debros
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=debros-node

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/home/debros

[Install]
WantedBy=multi-user.target
`

	if err := os.WriteFile("/etc/systemd/system/debros-node.service", []byte(nodeService), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to create node service: %v\n", err)
		os.Exit(1)
	}

	// Gateway service
	gatewayService := `[Unit]
Description=DeBros Gateway
After=debros-node.service
Wants=debros-node.service

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=/home/debros/src
ExecStart=/home/debros/bin/gateway
Environment=PATH=/usr/local/bin:/usr/bin:/bin
Environment=HOME=/home/debros
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=debros-gateway

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/home/debros

[Install]
WantedBy=multi-user.target
`

	if err := os.WriteFile("/etc/systemd/system/debros-gateway.service", []byte(gatewayService), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to create gateway service: %v\n", err)
		os.Exit(1)
	}

	// Reload systemd
	exec.Command("systemctl", "daemon-reload").Run()
	exec.Command("systemctl", "enable", "debros-node").Run()
	exec.Command("systemctl", "enable", "debros-gateway").Run()

	fmt.Printf("   ✓ Services created and enabled\n")
}

func startServices() {
	fmt.Printf("🚀 Starting services...\n")

	// Start node
	if err := exec.Command("systemctl", "start", "debros-node").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to start node service: %v\n", err)
	} else {
		fmt.Printf("   ✓ Node service started\n")
	}

	// Start gateway
	if err := exec.Command("systemctl", "start", "debros-gateway").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to start gateway service: %v\n", err)
	} else {
		fmt.Printf("   ✓ Gateway service started\n")
	}
}
