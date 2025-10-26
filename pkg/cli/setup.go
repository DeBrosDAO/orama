package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
		fmt.Fprintf(os.Stderr, "   Supported: Ubuntu 22.04/24.04, Debian 12\n")
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

	// Step 6: Initialize environments
	initializeEnvironments()

	// Step 7: Clone and build
	cloneAndBuild()

	// Step 8: Generate configs (interactive)
	generateConfigsInteractive(force)

	// Step 9: Create systemd services
	createSystemdServices()

	// Step 10: Start services
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

	// Add to sudoers
	sudoersContent := "debros ALL=(ALL) NOPASSWD:ALL\n"
	if err := os.WriteFile("/etc/sudoers.d/debros", []byte(sudoersContent), 0440); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to add debros to sudoers: %v\n", err)
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

func initializeEnvironments() {
	fmt.Printf("🔄 Initializing environments...\n")

	// Create .debros directory
	if err := os.MkdirAll("/home/debros/.debros", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to create .debros directory: %v\n", err)
		os.Exit(1)
	}

	// Create .debros/environments.json
	environmentsConfig := `{
	"node": {
		"bootstrap-peers": [],
		"join": "127.0.0.1:7001"
	},
	"gateway": {
		"bootstrap-peers": []
	}
}`
	if err := os.WriteFile("/home/debros/.debros/environments.json", []byte(environmentsConfig), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to create environments config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("   ✓ Environments initialized\n")
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
		cmd := exec.Command("sudo", "-u", "debros", "git", "clone", "--branch", "nightly", "https://github.com/DeBrosOfficial/network.git", "/home/debros/src")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to clone repo: %v\n", err)
			os.Exit(1)
		}
	}

	// Build
	fmt.Printf("   Building binaries...\n")
	cmd := exec.Command("sudo", "-u", "debros", "make", "build")
	cmd.Dir = "/home/debros/src"
	cmd.Env = append(os.Environ(), "PATH="+os.Getenv("PATH")+":/usr/local/go/bin")
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to build: %v\n%s\n", err, output)
		os.Exit(1)
	}

	// Copy binaries
	exec.Command("cp", "-r", "/home/debros/src/bin/", "/home/debros/bin/").Run()
	exec.Command("chown", "-R", "debros:debros", "/home/debros/bin").Run()
	exec.Command("chmod", "-R", "755", "/home/debros/bin").Run()

	fmt.Printf("   ✓ Built and installed\n")
}

func generateConfigsInteractive(force bool) {
	fmt.Printf("⚙️  Generating configurations...\n")

	// Prompt for bootstrap peers
	fmt.Printf("\n")
	fmt.Printf("Enter bootstrap peer multiaddresses (one per line, empty line to finish):\n")
	fmt.Printf("Format: /ip4/<IP>/tcp/<PORT>/p2p/<PEER_ID>\n")
	fmt.Printf("Example: /ip4/127.0.0.1/tcp/4001/p2p/12D3Koo...\n\n")

	reader := bufio.NewReader(os.Stdin)
	var bootstrapPeers []string
	for {
		fmt.Printf("Bootstrap peer: ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		bootstrapPeers = append(bootstrapPeers, line)
	}

	// Prompt for RQLite join address
	fmt.Printf("\nEnter RQLite join address (host:port, e.g., 10.0.1.5:7001): ")
	joinAddr, _ := reader.ReadString('\n')
	joinAddr = strings.TrimSpace(joinAddr)

	// Generate configs using network-cli
	bootstrapPeersStr := strings.Join(bootstrapPeers, ",")

	args := []string{
		"/home/debros/bin/network-cli", "config", "init",
		"--type", "node",
		"--bootstrap-peers", bootstrapPeersStr,
		"--join", joinAddr,
	}
	if force {
		args = append(args, "--force")
	}

	cmd := exec.Command("sudo", append([]string{"-u", "debros"}, args...)...)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to generate node config: %v\n", err)
	}

	// Generate gateway config
	cmd = exec.Command("sudo", "-u", "debros", "/home/debros/bin/network-cli", "config", "init", "--type", "gateway", "--bootstrap-peers", bootstrapPeersStr)
	if force {
		cmd.Args = append(cmd.Args, "--force")
	}
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to generate gateway config: %v\n", err)
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
ExecStart=/home/debros/bin/node --config /home/debros/.debros/environments.json
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
ExecStart=/home/debros/bin/gateway --config /home/debros/.debros/environments.json
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

	// Wait a bit
	exec.Command("sleep", "3").Run()

	// Start gateway
	if err := exec.Command("systemctl", "start", "debros-gateway").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to start gateway service: %v\n", err)
	} else {
		fmt.Printf("   ✓ Gateway service started\n")
	}
}
