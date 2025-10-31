package cli

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
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

	// Try to get and display peer ID
	peerID := getPeerID()
	if peerID != "" {
		fmt.Printf("🆔 Node Peer ID: %s\n\n", peerID)
	}

	fmt.Printf("Service Management:\n")
	fmt.Printf("  network-cli service status all\n")
	fmt.Printf("  network-cli service logs node --follow\n")
	fmt.Printf("  network-cli service restart gateway\n\n")
	fmt.Printf("Access DeBros User:\n")
	fmt.Printf("  sudo -u debros bash\n\n")

	// Check if HTTPS is enabled
	gatewayConfigPath := "/home/debros/.debros/gateway.yaml"
	httpsEnabled := false
	var domainName string
	if data, err := os.ReadFile(gatewayConfigPath); err == nil {
		var cfg config.Config
		if err := config.DecodeStrict(strings.NewReader(string(data)), &cfg); err == nil {
			// Try to parse as gateway config
			if strings.Contains(string(data), "enable_https: true") {
				httpsEnabled = true
				// Extract domain name from config
				lines := strings.Split(string(data), "\n")
				for _, line := range lines {
					if strings.HasPrefix(strings.TrimSpace(line), "domain_name:") {
						parts := strings.Split(line, ":")
						if len(parts) > 1 {
							domainName = strings.Trim(strings.TrimSpace(parts[1]), "\"")
						}
						break
					}
				}
			}
		}
	}

	fmt.Printf("Verify Installation:\n")
	if httpsEnabled && domainName != "" {
		fmt.Printf("  curl https://%s/health\n", domainName)
		fmt.Printf("  curl http://localhost:6001/health (HTTP fallback)\n")
	} else {
		fmt.Printf("  curl http://localhost:6001/health\n")
	}
	fmt.Printf("  curl http://localhost:5001/status\n\n")

	if httpsEnabled && domainName != "" {
		fmt.Printf("HTTPS Configuration:\n")
		fmt.Printf("  Domain: %s\n", domainName)
		fmt.Printf("  HTTPS endpoint: https://%s\n", domainName)
		fmt.Printf("  Certificate cache: /home/debros/.debros/tls-cache\n")
		fmt.Printf("  Certificates are automatically managed via Let's Encrypt (ACME)\n\n")
	}

	fmt.Printf("Anyone Relay (Anon):\n")
	fmt.Printf("  sudo systemctl status anon\n")
	fmt.Printf("  sudo tail -f /home/debros/.debros/logs/anon/notices.log\n")
	if httpsEnabled && domainName != "" {
		fmt.Printf("  Proxy endpoint: POST https://%s/v1/proxy/anon\n\n", domainName)
	} else {
		fmt.Printf("  Proxy endpoint: POST http://localhost:6001/v1/proxy/anon\n\n")
	}
}

// extractIPFromMultiaddr extracts the IP address from a multiaddr string
// Format: /ip4/51.83.128.181/tcp/4001/p2p/12D3KooW...
func extractIPFromMultiaddr(multiaddr string) string {
	if multiaddr == "" {
		return ""
	}

	// Split by "/ip4/"
	parts := strings.Split(multiaddr, "/ip4/")
	if len(parts) < 2 {
		return ""
	}

	// Get the part after "/ip4/"
	ipPart := parts[1]
	// Extract IP until the next "/"
	ipEnd := strings.Index(ipPart, "/")
	if ipEnd == -1 {
		// If no "/" found, the whole string might be the IP
		return strings.TrimSpace(ipPart)
	}

	ip := strings.TrimSpace(ipPart[:ipEnd])
	// Validate it looks like an IP address
	if net.ParseIP(ip) != nil {
		return ip
	}

	return ""
}

// getVPSIPv4Address gets the primary IPv4 address of the VPS
func getVPSIPv4Address() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			ip := ipNet.IP
			// Check if it's IPv4 and not a loopback address
			if ip.To4() != nil && !ip.IsLoopback() {
				return ip.String(), nil
			}
		}
	}

	return "", fmt.Errorf("could not find a non-loopback IPv4 address")
}

// getPeerID attempts to retrieve the peer ID from peer.info based on node type
func getPeerID() string {
	debrosDir := "/home/debros/.debros"
	nodeConfigPath := filepath.Join(debrosDir, "node.yaml")

	// Determine node type from config
	var nodeType string
	if file, err := os.Open(nodeConfigPath); err == nil {
		defer file.Close()
		var cfg config.Config
		if err := config.DecodeStrict(file, &cfg); err == nil {
			nodeType = cfg.Node.Type
		}
	}

	// Determine the peer.info path based on node type
	var peerInfoPath string
	if nodeType == "bootstrap" {
		peerInfoPath = filepath.Join(debrosDir, "bootstrap", "peer.info")
	} else {
		// Default to "node" directory for regular nodes
		peerInfoPath = filepath.Join(debrosDir, "node", "peer.info")
	}

	// Try to read from peer.info file
	if data, err := os.ReadFile(peerInfoPath); err == nil {
		peerInfo := strings.TrimSpace(string(data))
		// Extract peer ID from multiaddr format: /ip4/.../p2p/<peer-id>
		if strings.Contains(peerInfo, "/p2p/") {
			parts := strings.Split(peerInfo, "/p2p/")
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
		// If it's just the peer ID, return it
		if len(peerInfo) > 0 && !strings.Contains(peerInfo, "/") {
			return peerInfo
		}
	}

	return ""
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

// isPortAvailable checks if a port is available for binding
func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// checkPorts80And443 checks if ports 80 and 443 are available
func checkPorts80And443() (bool, string) {
	port80Available := isPortAvailable(80)
	port443Available := isPortAvailable(443)

	if !port80Available || !port443Available {
		var issues []string
		if !port80Available {
			issues = append(issues, "port 80")
		}
		if !port443Available {
			issues = append(issues, "port 443")
		}
		return false, strings.Join(issues, " and ")
	}

	return true, ""
}

// isValidDomain validates a domain name format
func isValidDomain(domain string) bool {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return false
	}

	// Basic validation: domain should contain at least one dot
	// and not start/end with dot or hyphen
	if !strings.Contains(domain, ".") {
		return false
	}

	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}

	if strings.HasPrefix(domain, "-") || strings.HasSuffix(domain, "-") {
		return false
	}

	// Check for valid characters (letters, numbers, dots, hyphens)
	for _, char := range domain {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '.' ||
			char == '-') {
			return false
		}
	}

	return true
}

// verifyDNSResolution verifies that a domain resolves to the VPS IP
func verifyDNSResolution(domain, expectedIP string) bool {
	ips, err := net.LookupIP(domain)
	if err != nil {
		return false
	}

	for _, ip := range ips {
		if ip.To4() != nil && ip.String() == expectedIP {
			return true
		}
	}

	return false
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
	fmt.Printf("⚙️  Generating configurations...\n\n")

	// Get VPS IPv4 address
	fmt.Printf("Detecting VPS IPv4 address...\n")
	vpsIP, err := getVPSIPv4Address()
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to detect IPv4 address: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Using 0.0.0.0 as fallback. You may need to edit config files manually.\n")
		vpsIP = "0.0.0.0"
	} else {
		fmt.Printf("   ✓ Detected IPv4 address: %s\n\n", vpsIP)
	}

	// Ask about node type
	fmt.Printf("What type of node is this?\n")
	fmt.Printf("  1. Bootstrap node (cluster leader)\n")
	fmt.Printf("  2. Regular node (joins existing cluster)\n")
	fmt.Printf("Enter choice (1 or 2): ")
	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.ToLower(strings.TrimSpace(choice))

	isBootstrap := choice == "1" || choice == "bootstrap" || choice == "b"

	var bootstrapPeers string
	if !isBootstrap {
		// Ask for bootstrap peer multiaddr
		fmt.Printf("\nEnter bootstrap peer multiaddr(s) (comma-separated if multiple):\n")
		fmt.Printf("Example: /ip4/192.168.1.100/tcp/4001/p2p/12D3KooW...\n")
		fmt.Printf("Bootstrap peer(s): ")
		bootstrapPeers, _ = reader.ReadString('\n')
		bootstrapPeers = strings.TrimSpace(bootstrapPeers)
	}

	nodeConfigPath := "/home/debros/.debros/node.yaml"
	gatewayPath := "/home/debros/.debros/gateway.yaml"

	// Check if node.yaml already exists
	nodeExists := false
	if _, err := os.Stat(nodeConfigPath); err == nil {
		nodeExists = true
		if !force {
			fmt.Printf("\n   ℹ️  node.yaml already exists, will not overwrite\n")
		}
	}

	// Generate node config
	if !nodeExists || force {
		var nodeConfig string
		if isBootstrap {
			nodeConfig = generateBootstrapConfigWithIP("bootstrap", "", 4001, 5001, 7001, vpsIP)
		} else {
			// Extract IP from bootstrap peer multiaddr for rqlite_join_address
			// Use first bootstrap peer if multiple provided
			var joinAddr string
			if bootstrapPeers != "" {
				firstPeer := strings.Split(bootstrapPeers, ",")[0]
				firstPeer = strings.TrimSpace(firstPeer)
				extractedIP := extractIPFromMultiaddr(firstPeer)
				if extractedIP != "" {
					joinAddr = fmt.Sprintf("%s:7001", extractedIP)
				} else {
					joinAddr = "localhost:7001"
				}
			} else {
				joinAddr = "localhost:7001"
			}
			nodeConfig = generateNodeConfigWithIP("node", "", 4001, 5001, 7001, joinAddr, bootstrapPeers, vpsIP)
		}

		// Write node config
		if err := os.WriteFile(nodeConfigPath, []byte(nodeConfig), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to write node config: %v\n", err)
			os.Exit(1)
		}
		// Fix ownership
		exec.Command("chown", "debros:debros", nodeConfigPath).Run()
		fmt.Printf("   ✓ Node config created: %s\n", nodeConfigPath)
	}

	// Generate gateway config
	gatewayExists := false
	if _, err := os.Stat(gatewayPath); err == nil {
		gatewayExists = true
		if !force {
			fmt.Printf("   ℹ️  gateway.yaml already exists, skipping creation\n")
		}
	}

	if !gatewayExists || force {
		// Prompt for domain and HTTPS configuration
		var domain string
		var enableHTTPS bool
		var tlsCacheDir string

		fmt.Printf("\n🌐 Domain and HTTPS Configuration\n")
		fmt.Printf("Would you like to configure HTTPS with a domain name? (yes/no) [default: no]: ")
		response, _ := reader.ReadString('\n')
		response = strings.ToLower(strings.TrimSpace(response))

		if response == "yes" || response == "y" {
			// Check if ports 80 and 443 are available
			portsAvailable, portIssues := checkPorts80And443()
			if !portsAvailable {
				fmt.Fprintf(os.Stderr, "\n⚠️  Cannot enable HTTPS: %s is already in use\n", portIssues)
				fmt.Fprintf(os.Stderr, "   You will need to configure HTTPS manually if you want to use a domain.\n")
				fmt.Fprintf(os.Stderr, "   Continuing without HTTPS configuration...\n\n")
				enableHTTPS = false
			} else {
				enableHTTPS = true

				// Prompt for domain name
				for {
					fmt.Printf("\nEnter your domain name (e.g., example.com): ")
					domainInput, _ := reader.ReadString('\n')
					domain = strings.TrimSpace(domainInput)

					if domain == "" {
						fmt.Printf("   Domain name cannot be empty. Skipping HTTPS configuration.\n")
						enableHTTPS = false
						break
					}

					if !isValidDomain(domain) {
						fmt.Printf("   ❌ Invalid domain format. Please enter a valid domain name.\n")
						continue
					}

					// Verify DNS is configured
					fmt.Printf("\n   Verifying DNS configuration...\n")
					fmt.Printf("   Please ensure your domain %s points to this server's IP (%s)\n", domain, vpsIP)
					fmt.Printf("   Have you configured the DNS record? (yes/no): ")
					dnsResponse, _ := reader.ReadString('\n')
					dnsResponse = strings.ToLower(strings.TrimSpace(dnsResponse))

					if dnsResponse == "yes" || dnsResponse == "y" {
						// Try to verify DNS resolution
						fmt.Printf("   Checking DNS resolution...\n")
						if verifyDNSResolution(domain, vpsIP) {
							fmt.Printf("   ✓ DNS is correctly configured\n")
							break
						} else {
							fmt.Printf("   ⚠️  DNS does not resolve to this server's IP (%s)\n", vpsIP)
							fmt.Printf("   DNS may still be propagating. Continue anyway? (yes/no): ")
							continueResponse, _ := reader.ReadString('\n')
							continueResponse = strings.ToLower(strings.TrimSpace(continueResponse))
							if continueResponse == "yes" || continueResponse == "y" {
								fmt.Printf("   Continuing with domain configuration (DNS may need time to propagate)\n")
								break
							}
							// User chose not to continue, ask for domain again
							fmt.Printf("   Please configure DNS and try again, or press Enter to skip HTTPS\n")
							continue
						}
					} else {
						fmt.Printf("   Please configure DNS first. Type 'skip' to skip HTTPS configuration: ")
						skipResponse, _ := reader.ReadString('\n')
						skipResponse = strings.ToLower(strings.TrimSpace(skipResponse))
						if skipResponse == "skip" {
							enableHTTPS = false
							domain = ""
							break
						}
						continue
					}
				}

				// Set TLS cache directory if HTTPS is enabled
				if enableHTTPS && domain != "" {
					tlsCacheDir = "/home/debros/.debros/tls-cache"
					// Create TLS cache directory
					if err := os.MkdirAll(tlsCacheDir, 0755); err != nil {
						fmt.Fprintf(os.Stderr, "⚠️  Failed to create TLS cache directory: %v\n", err)
					} else {
						exec.Command("chown", "-R", "debros:debros", tlsCacheDir).Run()
						fmt.Printf("   ✓ TLS cache directory created: %s\n", tlsCacheDir)
					}
				}
			}
		}

		// Gateway config should include bootstrap peers if this is a regular node
		// (bootstrap nodes don't need bootstrap peers since they are the bootstrap)
		gatewayConfig := generateGatewayConfigDirect(bootstrapPeers, enableHTTPS, domain, tlsCacheDir)
		if err := os.WriteFile(gatewayPath, []byte(gatewayConfig), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to write gateway config: %v\n", err)
			os.Exit(1)
		}
		// Fix ownership
		exec.Command("chown", "debros:debros", gatewayPath).Run()
		fmt.Printf("   ✓ Gateway config created: %s\n", gatewayPath)
	}

	fmt.Printf("\n   ✓ Configurations ready\n")
}

// generateBootstrapConfigWithIP generates a bootstrap config with actual IP address
func generateBootstrapConfigWithIP(name, id string, listenPort, rqliteHTTPPort, rqliteRaftPort int, ipAddr string) string {
	nodeID := id
	if nodeID == "" {
		nodeID = "bootstrap"
	}

	dataDir := "/home/debros/.debros/bootstrap"

	return fmt.Sprintf(`node:
  id: "%s"
  type: "bootstrap"
  listen_addresses:
    - "/ip4/%s/tcp/%d"
  data_dir: "%s"
  max_connections: 50

database:
  data_dir: "%s/rqlite"
  replication_factor: 3
  shard_count: 16
  max_database_size: 1073741824
  backup_interval: "24h"
  rqlite_port: %d
  rqlite_raft_port: %d
  rqlite_join_address: ""
  cluster_sync_interval: "30s"
  peer_inactivity_limit: "24h"
  min_cluster_size: 1

discovery:
  bootstrap_peers: []
  discovery_interval: "15s"
  bootstrap_port: %d
  http_adv_address: "%s:%d"
  raft_adv_address: "%s:%d"
  node_namespace: "default"

security:
  enable_tls: false

logging:
  level: "info"
  format: "console"
`, nodeID, ipAddr, listenPort, dataDir, dataDir, rqliteHTTPPort, rqliteRaftPort, 4001, ipAddr, rqliteHTTPPort, ipAddr, rqliteRaftPort)
}

// generateNodeConfigWithIP generates a node config with actual IP address
func generateNodeConfigWithIP(name, id string, listenPort, rqliteHTTPPort, rqliteRaftPort int, joinAddr, bootstrapPeers, ipAddr string) string {
	nodeID := id
	if nodeID == "" {
		nodeID = fmt.Sprintf("node-%d", time.Now().Unix())
	}

	dataDir := "/home/debros/.debros/node"

	// Parse bootstrap peers
	var peers []string
	if bootstrapPeers != "" {
		for _, p := range strings.Split(bootstrapPeers, ",") {
			if p = strings.TrimSpace(p); p != "" {
				peers = append(peers, p)
			}
		}
	}

	var peersYAML strings.Builder
	if len(peers) == 0 {
		peersYAML.WriteString("  bootstrap_peers: []")
	} else {
		peersYAML.WriteString("  bootstrap_peers:\n")
		for _, p := range peers {
			fmt.Fprintf(&peersYAML, "    - \"%s\"\n", p)
		}
	}

	if joinAddr == "" {
		joinAddr = "localhost:7001"
	}

	return fmt.Sprintf(`node:
  id: "%s"
  type: "node"
  listen_addresses:
    - "/ip4/%s/tcp/%d"
  data_dir: "%s"
  max_connections: 50

database:
  data_dir: "%s/rqlite"
  replication_factor: 3
  shard_count: 16
  max_database_size: 1073741824
  backup_interval: "24h"
  rqlite_port: %d
  rqlite_raft_port: %d
  rqlite_join_address: "%s"
  cluster_sync_interval: "30s"
  peer_inactivity_limit: "24h"
  min_cluster_size: 1

discovery:
%s
  discovery_interval: "15s"
  bootstrap_port: %d
  http_adv_address: "%s:%d"
  raft_adv_address: "%s:%d"
  node_namespace: "default"

security:
  enable_tls: false

logging:
  level: "info"
  format: "console"
`, nodeID, ipAddr, listenPort, dataDir, dataDir, rqliteHTTPPort, rqliteRaftPort, joinAddr, peersYAML.String(), 4001, ipAddr, rqliteHTTPPort, ipAddr, rqliteRaftPort)
}

// generateGatewayConfigDirect generates gateway config directly
func generateGatewayConfigDirect(bootstrapPeers string, enableHTTPS bool, domain, tlsCacheDir string) string {
	var peers []string
	if bootstrapPeers != "" {
		for _, p := range strings.Split(bootstrapPeers, ",") {
			if p = strings.TrimSpace(p); p != "" {
				peers = append(peers, p)
			}
		}
	}

	var peersYAML strings.Builder
	if len(peers) == 0 {
		peersYAML.WriteString("bootstrap_peers: []")
	} else {
		peersYAML.WriteString("bootstrap_peers:\n")
		for _, p := range peers {
			fmt.Fprintf(&peersYAML, "  - \"%s\"\n", p)
		}
	}

	var httpsYAML strings.Builder
	if enableHTTPS && domain != "" {
		fmt.Fprintf(&httpsYAML, "enable_https: true\n")
		fmt.Fprintf(&httpsYAML, "domain_name: \"%s\"\n", domain)
		if tlsCacheDir != "" {
			fmt.Fprintf(&httpsYAML, "tls_cache_dir: \"%s\"\n", tlsCacheDir)
		}
	} else {
		fmt.Fprintf(&httpsYAML, "enable_https: false\n")
	}

	return fmt.Sprintf(`listen_addr: ":6001"
client_namespace: "default"
rqlite_dsn: ""
%s
%s
`, peersYAML.String(), httpsYAML.String())
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

# Allow binding to privileged ports (80, 443) for HTTPS/ACME
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

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
	fmt.Printf("🚀 Starting/Restarting services...\n")

	// Helper function to start or restart a service
	startOrRestartService := func(serviceName string) {
		// Check if service is active/running
		checkCmd := exec.Command("systemctl", "is-active", "--quiet", serviceName)
		isRunning := checkCmd.Run() == nil

		if isRunning {
			// Service is running, restart it
			fmt.Printf("   Restarting %s service...\n", serviceName)
			if err := exec.Command("systemctl", "restart", serviceName).Run(); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Failed to restart %s service: %v\n", serviceName, err)
			} else {
				fmt.Printf("   ✓ %s service restarted\n", serviceName)
			}
		} else {
			// Service is not running, start it
			fmt.Printf("   Starting %s service...\n", serviceName)
			if err := exec.Command("systemctl", "start", serviceName).Run(); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Failed to start %s service: %v\n", serviceName, err)
			} else {
				fmt.Printf("   ✓ %s service started\n", serviceName)
			}
		}
	}

	// Start or restart node service
	startOrRestartService("debros-node")

	// Start or restart gateway service
	startOrRestartService("debros-gateway")

	// Also restart Anon service if it's running (to pick up any config changes)
	if exec.Command("systemctl", "is-active", "--quiet", "anon").Run() == nil {
		fmt.Printf("   Restarting Anon service to pick up config changes...\n")
		if err := exec.Command("systemctl", "restart", "anon").Run(); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to restart Anon service: %v\n", err)
		} else {
			fmt.Printf("   ✓ Anon service restarted\n")
		}
	}
}
