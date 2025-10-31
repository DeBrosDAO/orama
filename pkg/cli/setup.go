package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	fmt.Printf("  5. Install Anyone Relay (Anon) for anonymous networking\n")
	fmt.Printf("  6. Create directories (/home/debros/bin, /home/debros/src)\n")
	fmt.Printf("  7. Clone and build DeBros Network\n")
	fmt.Printf("  8. Generate configuration files\n")
	fmt.Printf("  9. Create systemd services (debros-node, debros-gateway)\n")
	fmt.Printf(" 10. Start and enable services\n")
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

	// Step 4.5: Install Anon (Anyone relay)
	installAnon()

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
	fmt.Printf("Access DeBros User:\n")
	fmt.Printf("  sudo -u debros bash\n\n")
	fmt.Printf("Verify Installation:\n")
	fmt.Printf("  curl http://localhost:6001/health\n")
	fmt.Printf("  curl http://localhost:5001/status\n\n")
	fmt.Printf("Anyone Relay (Anon):\n")
	fmt.Printf("  sudo systemctl status anon\n")
	fmt.Printf("  sudo tail -f /home/debros/.debros/logs/anon/notices.log\n")
	fmt.Printf("  Proxy endpoint: POST http://localhost:6001/v1/proxy/anon\n\n")
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

func promptBranch() string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("   Select branch (main/nightly) [default: main]: ")
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	if response == "nightly" {
		return "nightly"
	}
	// Default to main for anything else (including empty)
	return "main"
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
	userExists := false
	if _, err := exec.Command("id", "debros").CombinedOutput(); err == nil {
		fmt.Printf("   ✓ User 'debros' already exists\n")
		userExists = true
	} else {
		// Create user
		cmd := exec.Command("useradd", "-r", "-m", "-s", "/bin/bash", "-d", "/home/debros", "debros")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to create user 'debros': %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("   ✓ Created user 'debros'\n")
	}

	// Get the user who invoked sudo (the actual user, not root)
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser == "" {
		// If not running via sudo, skip sudoers setup
		return
	}

	// Create sudoers rule to allow passwordless access to debros user
	sudoersRule := fmt.Sprintf("%s ALL=(debros) NOPASSWD: ALL\n", sudoUser)
	sudoersFile := "/etc/sudoers.d/debros-access"

	// Check if sudoers rule already exists
	if existing, err := os.ReadFile(sudoersFile); err == nil {
		if strings.Contains(string(existing), sudoUser) {
			if !userExists {
				fmt.Printf("   ✓ Sudoers access configured\n")
			}
			return
		}
	}

	// Write sudoers rule
	if err := os.WriteFile(sudoersFile, []byte(sudoersRule), 0440); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to create sudoers rule: %v\n", err)
		fmt.Fprintf(os.Stderr, "   You can manually switch to debros using: sudo -u debros bash\n")
		return
	}

	// Validate the sudoers file
	if err := exec.Command("visudo", "-c", "-f", sudoersFile).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Sudoers rule validation failed, removing file\n")
		os.Remove(sudoersFile)
		return
	}

	fmt.Printf("   ✓ Sudoers access configured\n")
	fmt.Printf("   You can now run: sudo -u debros bash\n")
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

func installAnon() {
	fmt.Printf("🔐 Installing Anyone Relay (Anon)...\n")

	// Check if already installed
	if _, err := exec.LookPath("anon"); err == nil {
		fmt.Printf("   ✓ Anon already installed\n")
		configureAnonLogs()
		configureFirewallForAnon()
		return
	}

	// Check Ubuntu version - Ubuntu 25.04 is not yet supported by Anon repository
	osInfo := detectLinuxDistro()
	if strings.Contains(strings.ToLower(osInfo), "ubuntu 25.04") || strings.Contains(strings.ToLower(osInfo), "plucky") {
		fmt.Fprintf(os.Stderr, "⚠️  Ubuntu 25.04 (Plucky) is not yet supported by Anon repository\n")
		fmt.Fprintf(os.Stderr, "   Anon installation will be skipped. The gateway will work without it,\n")
		fmt.Fprintf(os.Stderr, "   but anonymous proxy functionality will not be available.\n")
		fmt.Fprintf(os.Stderr, "   You can manually install Anon later when support is added:\n")
		fmt.Fprintf(os.Stderr, "   sudo /bin/bash -c \"$(curl -fsSL https://raw.githubusercontent.com/anyone-protocol/anon-install/refs/heads/main/install.sh)\"\n\n")
		return
	}

	// Install via official installation script (from GitHub)
	fmt.Printf("   Installing Anon using official installation script...\n")
	fmt.Printf("   Note: The installation script may prompt for configuration\n")

	// Clean up any old APT repository files from previous installation attempts
	gpgKeyPath := "/usr/share/keyrings/anyone-archive-keyring.gpg"
	repoPath := "/etc/apt/sources.list.d/anyone.list"
	if _, err := os.Stat(gpgKeyPath); err == nil {
		fmt.Printf("   Removing old GPG key file...\n")
		os.Remove(gpgKeyPath)
	}
	if _, err := os.Stat(repoPath); err == nil {
		fmt.Printf("   Removing old repository file...\n")
		os.Remove(repoPath)
	}

	// Preseed debconf before installation
	fmt.Printf("   Pre-accepting Anon terms and conditions...\n")
	preseedCmd := exec.Command("sh", "-c", `echo "anon anon/terms boolean true" | debconf-set-selections`)
	preseedCmd.Run() // Ignore errors, preseed might not be critical

	// Create anonrc directory and file with AgreeToTerms before installation
	// This ensures terms are accepted even if the post-install script checks the file
	anonrcDir := "/etc/anon"
	anonrcPath := "/etc/anon/anonrc"
	if err := os.MkdirAll(anonrcDir, 0755); err == nil {
		if _, err := os.Stat(anonrcPath); os.IsNotExist(err) {
			// Create file with AgreeToTerms already set
			os.WriteFile(anonrcPath, []byte("AgreeToTerms 1\n"), 0644)
		}
	}

	// Create terms-agreement files in multiple possible locations
	// Anon might check for these files to verify terms acceptance
	termsLocations := []string{
		"/var/lib/anon/terms-agreement",
		"/usr/share/anon/terms-agreement",
		"/usr/share/keyrings/anon/terms-agreement",
		"/usr/share/keyrings/anyone-terms-agreed",
	}
	for _, loc := range termsLocations {
		dir := filepath.Dir(loc)
		if err := os.MkdirAll(dir, 0755); err == nil {
			os.WriteFile(loc, []byte("agreed\n"), 0644)
		}
	}

	// Use the official installation script from GitHub
	// Rely on debconf preseed and file-based acceptance methods
	// If prompts still appear, pipe a few "yes" responses (not infinite)
	installScriptURL := "https://raw.githubusercontent.com/anyone-protocol/anon-install/refs/heads/main/install.sh"
	// Pipe multiple "yes" responses (but limited) in case of multiple prompts
	yesResponses := strings.Repeat("yes\n", 10) // 10 "yes" responses should be enough
	cmd := exec.Command("sh", "-c", fmt.Sprintf("curl -fsSL %s | bash", installScriptURL))
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	cmd.Stdin = strings.NewReader(yesResponses)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Anon installation failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "   The gateway will work without Anon, but anonymous proxy functionality will not be available.\n")
		fmt.Fprintf(os.Stderr, "   You can manually install Anon later:\n")
		fmt.Fprintf(os.Stderr, "   sudo /bin/bash -c \"$(curl -fsSL https://raw.githubusercontent.com/anyone-protocol/anon-install/refs/heads/main/install.sh)\"\n")
		return // Continue setup without Anon
	}

	// Verify installation
	if _, err := exec.LookPath("anon"); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Anon installation verification failed: binary not found in PATH\n")
		fmt.Fprintf(os.Stderr, "   Continuing setup without Anon...\n")
		return // Continue setup without Anon
	}

	fmt.Printf("   ✓ Anon installed\n")

	// Configure with sensible defaults
	configureAnonDefaults()

	// Configure logs
	configureAnonLogs()

	// Configure firewall
	configureFirewallForAnon()

	// Enable and start service
	fmt.Printf("   Enabling Anon service...\n")
	enableCmd := exec.Command("systemctl", "enable", "anon")
	if output, err := enableCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to enable Anon service: %v\n", err)
		if len(output) > 0 {
			fmt.Fprintf(os.Stderr, "   Output: %s\n", string(output))
		}
	}

	startCmd := exec.Command("systemctl", "start", "anon")
	if output, err := startCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to start Anon service: %v\n", err)
		if len(output) > 0 {
			fmt.Fprintf(os.Stderr, "   Output: %s\n", string(output))
		}
		fmt.Fprintf(os.Stderr, "   Check service status: systemctl status anon\n")
	} else {
		fmt.Printf("   ✓ Anon service started\n")
	}

	// Verify service is running
	if exec.Command("systemctl", "is-active", "--quiet", "anon").Run() == nil {
		fmt.Printf("   ✓ Anon service is active\n")
	} else {
		fmt.Fprintf(os.Stderr, "⚠️  Anon service may not be running. Check: systemctl status anon\n")
	}
}

func configureAnonDefaults() {
	fmt.Printf("   Configuring Anon with default settings...\n")

	hostname := "debros-node"
	if h, err := os.Hostname(); err == nil && h != "" {
		hostname = strings.Split(h, ".")[0]
	}

	anonrcPath := "/etc/anon/anonrc"
	if _, err := os.Stat(anonrcPath); err == nil {
		// Backup existing config
		exec.Command("cp", anonrcPath, anonrcPath+".bak").Run()

		// Read existing config
		data, err := os.ReadFile(anonrcPath)
		if err != nil {
			return
		}
		config := string(data)

		// Add settings if not present
		if !strings.Contains(config, "Nickname") {
			config += fmt.Sprintf("\nNickname %s\n", hostname)
		}
		if !strings.Contains(config, "ControlPort") {
			config += "ControlPort 9051\n"
		}
		if !strings.Contains(config, "SocksPort") {
			config += "SocksPort 9050\n"
		}
		// Auto-accept terms to avoid interactive prompts
		if !strings.Contains(config, "AgreeToTerms") {
			config += "AgreeToTerms 1\n"
		}

		// Write back
		os.WriteFile(anonrcPath, []byte(config), 0644)

		fmt.Printf("   Nickname: %s\n", hostname)
		fmt.Printf("   ORPort: 9001 (default)\n")
		fmt.Printf("   ControlPort: 9051\n")
		fmt.Printf("   SOCKSPort: 9050\n")
		fmt.Printf("   AgreeToTerms: 1 (auto-accepted)\n")
	}
}

func configureAnonLogs() {
	fmt.Printf("   Configuring Anon logs...\n")

	// Create log directory
	logDir := "/home/debros/.debros/logs/anon"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to create log directory: %v\n", err)
		return
	}

	// Change ownership to debian-anon (the user anon runs as)
	exec.Command("chown", "-R", "debian-anon:debian-anon", logDir).Run()

	// Update anonrc if it exists
	anonrcPath := "/etc/anon/anonrc"
	if _, err := os.Stat(anonrcPath); err == nil {
		// Read current config
		data, err := os.ReadFile(anonrcPath)
		if err == nil {
			config := string(data)

			// Replace log file path
			newConfig := strings.ReplaceAll(config,
				"Log notice file /var/log/anon/notices.log",
				"Log notice file /home/debros/.debros/logs/anon/notices.log")

			// Write back
			if err := os.WriteFile(anonrcPath, []byte(newConfig), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Failed to update anonrc: %v\n", err)
			} else {
				fmt.Printf("   ✓ Anon logs configured to %s\n", logDir)

				// Restart anon service if running
				if exec.Command("systemctl", "is-active", "--quiet", "anon").Run() == nil {
					exec.Command("systemctl", "restart", "anon").Run()
				}
			}
		}
	}
}

func configureFirewallForAnon() {
	fmt.Printf("   Checking firewall configuration...\n")

	// Check for UFW
	if _, err := exec.LookPath("ufw"); err == nil {
		output, _ := exec.Command("ufw", "status").CombinedOutput()
		if strings.Contains(string(output), "Status: active") {
			fmt.Printf("   Adding UFW rules for Anon...\n")
			exec.Command("ufw", "allow", "9001/tcp", "comment", "Anon ORPort").Run()
			exec.Command("ufw", "allow", "9051/tcp", "comment", "Anon ControlPort").Run()
			fmt.Printf("   ✓ UFW rules added\n")
			return
		}
	}

	// Check for firewalld
	if _, err := exec.LookPath("firewall-cmd"); err == nil {
		output, _ := exec.Command("firewall-cmd", "--state").CombinedOutput()
		if strings.Contains(string(output), "running") {
			fmt.Printf("   Adding firewalld rules for Anon...\n")
			exec.Command("firewall-cmd", "--permanent", "--add-port=9001/tcp").Run()
			exec.Command("firewall-cmd", "--permanent", "--add-port=9051/tcp").Run()
			exec.Command("firewall-cmd", "--reload").Run()
			fmt.Printf("   ✓ firewalld rules added\n")
			return
		}
	}

	// Check for iptables
	if _, err := exec.LookPath("iptables"); err == nil {
		output, _ := exec.Command("iptables", "-L", "-n").CombinedOutput()
		if strings.Contains(string(output), "Chain INPUT") {
			fmt.Printf("   Adding iptables rules for Anon...\n")
			exec.Command("iptables", "-A", "INPUT", "-p", "tcp", "--dport", "9001", "-j", "ACCEPT", "-m", "comment", "--comment", "Anon ORPort").Run()
			exec.Command("iptables", "-A", "INPUT", "-p", "tcp", "--dport", "9051", "-j", "ACCEPT", "-m", "comment", "--comment", "Anon ControlPort").Run()

			// Try to save rules
			if _, err := exec.LookPath("netfilter-persistent"); err == nil {
				exec.Command("netfilter-persistent", "save").Run()
			} else if _, err := exec.LookPath("iptables-save"); err == nil {
				cmd := exec.Command("sh", "-c", "iptables-save > /etc/iptables/rules.v4")
				cmd.Run()
			}
			fmt.Printf("   ✓ iptables rules added\n")
			return
		}
	}

	fmt.Printf("   No active firewall detected\n")
}

func setupDirectories() {
	fmt.Printf("📁 Creating directories...\n")

	dirs := []string{
		"/home/debros/bin",
		"/home/debros/src",
		"/home/debros/.debros",
		"/home/debros/go",     // Go module cache directory
		"/home/debros/.cache", // Go build cache directory
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

	// Prompt for branch selection
	branch := promptBranch()
	fmt.Printf("   Using branch: %s\n", branch)

	// Check if already cloned
	if _, err := os.Stat("/home/debros/src/.git"); err == nil {
		fmt.Printf("   Updating repository...\n")

		// Check current branch and switch if needed
		currentBranchCmd := exec.Command("sudo", "-u", "debros", "git", "-C", "/home/debros/src", "rev-parse", "--abbrev-ref", "HEAD")
		if output, err := currentBranchCmd.Output(); err == nil {
			currentBranch := strings.TrimSpace(string(output))
			if currentBranch != branch {
				fmt.Printf("   Switching from %s to %s...\n", currentBranch, branch)
				// Fetch the target branch first (needed for shallow clones)
				exec.Command("sudo", "-u", "debros", "git", "-C", "/home/debros/src", "fetch", "origin", branch).Run()
				// Checkout the selected branch
				checkoutCmd := exec.Command("sudo", "-u", "debros", "git", "-C", "/home/debros/src", "checkout", branch)
				if err := checkoutCmd.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "⚠️  Failed to switch branch: %v\n", err)
				}
			}
		}

		// Pull latest changes
		cmd := exec.Command("sudo", "-u", "debros", "git", "-C", "/home/debros/src", "pull", "origin", branch)
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to update repo: %v\n", err)
		}
	} else {
		fmt.Printf("   Cloning repository...\n")
		cmd := exec.Command("sudo", "-u", "debros", "git", "clone", "--branch", branch, "--depth", "1", "https://github.com/DeBrosOfficial/network.git", "/home/debros/src")
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
	// Set HOME so Go knows where to create module cache
	cmd := exec.Command("sudo", "--preserve-env=PATH", "-u", "debros", "make", "build")
	cmd.Dir = "/home/debros/src"
	cmd.Env = append(os.Environ(), "HOME=/home/debros", "PATH="+os.Getenv("PATH")+":/usr/local/go/bin")
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

	bootstrapPath := "/home/debros/.debros/bootstrap.yaml"
	nodeConfigPath := "/home/debros/.debros/node.yaml"
	gatewayPath := "/home/debros/.debros/gateway.yaml"

	// Check if node.yaml already exists
	nodeExists := false
	if _, err := os.Stat(nodeConfigPath); err == nil {
		nodeExists = true
		fmt.Printf("   ℹ️  node.yaml already exists, will not overwrite\n")
	}

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
	bootstrapCreated := (err == nil)

	if err != nil {
		// Check if bootstrap.yaml already exists (config init failed because it exists)
		if _, statErr := os.Stat(bootstrapPath); statErr == nil {
			fmt.Printf("   ℹ️  bootstrap.yaml already exists, skipping creation\n")
			bootstrapCreated = true
		} else {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to generate bootstrap config: %v\n", err)
			if len(output) > 0 {
				fmt.Fprintf(os.Stderr, "   Output: %s\n", string(output))
			}
		}
	} else {
		fmt.Printf("   ✓ Bootstrap node config created\n")
	}

	// Rename bootstrap.yaml to node.yaml only if node.yaml doesn't exist
	if !nodeExists && bootstrapCreated {
		// Check if bootstrap.yaml exists before renaming
		if _, err := os.Stat(bootstrapPath); err == nil {
			renameCmd := exec.Command("sudo", "-u", "debros", "mv", bootstrapPath, nodeConfigPath)
			if err := renameCmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Failed to rename config: %v\n", err)
			} else {
				fmt.Printf("   ✓ Renamed bootstrap.yaml to node.yaml\n")
			}
		}
	} else if nodeExists {
		// If node.yaml exists, we can optionally remove bootstrap.yaml if it was just created
		if bootstrapCreated && !force {
			// Clean up bootstrap.yaml if it was just created but node.yaml already exists
			if _, err := os.Stat(bootstrapPath); err == nil {
				exec.Command("sudo", "-u", "debros", "rm", "-f", bootstrapPath).Run()
			}
		}
		fmt.Printf("   ℹ️  Using existing node.yaml\n")
	}

	// Generate gateway config with explicit empty bootstrap peers
	// Check if gateway.yaml already exists
	gatewayExists := false
	if _, err := os.Stat(gatewayPath); err == nil {
		gatewayExists = true
		if !force {
			fmt.Printf("   ℹ️  gateway.yaml already exists, skipping creation\n")
		}
	}

	if !gatewayExists || force {
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
			// Check if gateway.yaml already exists (config init failed because it exists)
			if _, statErr := os.Stat(gatewayPath); statErr == nil {
				fmt.Printf("   ℹ️  gateway.yaml already exists, skipping creation\n")
			} else {
				fmt.Fprintf(os.Stderr, "⚠️  Failed to generate gateway config: %v\n", err)
				if len(output) > 0 {
					fmt.Fprintf(os.Stderr, "   Output: %s\n", string(output))
				}
			}
		} else {
			fmt.Printf("   ✓ Gateway config created\n")
		}
	}

	fmt.Printf("   ✓ Configurations ready\n")
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
