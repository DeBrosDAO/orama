package cli

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	fmt.Printf("  6. Install Olric cache server\n")
	fmt.Printf("  7. Install IPFS (Kubo) and IPFS Cluster\n")
	fmt.Printf("  8. Create directories (/home/debros/bin, /home/debros/src)\n")
	fmt.Printf("  9. Clone and build DeBros Network\n")
	fmt.Printf(" 10. Generate configuration files\n")
	fmt.Printf(" 11. Create systemd services (debros-ipfs, debros-ipfs-cluster, debros-node, debros-gateway, debros-olric)\n")
	fmt.Printf(" 12. Start and enable services\n")
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

	// Step 4.6: Install Olric cache server
	installOlric()

	// Step 4.7: Install IPFS and IPFS Cluster
	installIPFS()

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

	// Display IPFS Cluster information
	fmt.Printf("IPFS Cluster Setup:\n")
	fmt.Printf("  Each node runs its own IPFS Cluster peer\n")
	fmt.Printf("  Cluster peers use CRDT consensus for automatic discovery\n")
	fmt.Printf("  To verify cluster is working:\n")
	fmt.Printf("    sudo -u debros ipfs-cluster-ctl --host http://localhost:9094 peers ls\n")
	fmt.Printf("  You should see all cluster peers listed\n\n")

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

// promptDomainForHTTPS prompts for domain name and verifies DNS configuration
func promptDomainForHTTPS(reader *bufio.Reader, vpsIP string) string {
	for {
		fmt.Printf("\nEnter your domain name (e.g., example.com): ")
		domainInput, _ := reader.ReadString('\n')
		domain := strings.TrimSpace(domainInput)

		if domain == "" {
			fmt.Printf("   Domain name cannot be empty. Skipping HTTPS configuration.\n")
			return ""
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
				return domain
			} else {
				fmt.Printf("   ⚠️  DNS does not resolve to this server's IP (%s)\n", vpsIP)
				fmt.Printf("   DNS may still be propagating. Continue anyway? (yes/no): ")
				continueResponse, _ := reader.ReadString('\n')
				continueResponse = strings.ToLower(strings.TrimSpace(continueResponse))
				if continueResponse == "yes" || continueResponse == "y" {
					fmt.Printf("   Continuing with domain configuration (DNS may need time to propagate)\n")
					return domain
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
				return ""
			}
			continue
		}
	}
}

// updateGatewayConfigWithHTTPS updates an existing gateway.yaml file with HTTPS settings
func updateGatewayConfigWithHTTPS(gatewayPath, domain string) error {
	// Read existing config
	data, err := os.ReadFile(gatewayPath)
	if err != nil {
		return fmt.Errorf("failed to read gateway config: %w", err)
	}

	configContent := string(data)
	tlsCacheDir := "/home/debros/.debros/tls-cache"

	// Check if HTTPS is already enabled
	if strings.Contains(configContent, "enable_https: true") {
		// Update existing HTTPS settings
		lines := strings.Split(configContent, "\n")
		var updatedLines []string
		domainUpdated := false
		cacheDirUpdated := false

		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "enable_https:") {
				updatedLines = append(updatedLines, "enable_https: true")
			} else if strings.HasPrefix(trimmed, "domain_name:") {
				updatedLines = append(updatedLines, fmt.Sprintf("domain_name: \"%s\"", domain))
				domainUpdated = true
			} else if strings.HasPrefix(trimmed, "tls_cache_dir:") {
				updatedLines = append(updatedLines, fmt.Sprintf("tls_cache_dir: \"%s\"", tlsCacheDir))
				cacheDirUpdated = true
			} else {
				updatedLines = append(updatedLines, line)
			}
		}

		// Add missing fields if not found
		if !domainUpdated {
			updatedLines = append(updatedLines, fmt.Sprintf("domain_name: \"%s\"", domain))
		}
		if !cacheDirUpdated {
			updatedLines = append(updatedLines, fmt.Sprintf("tls_cache_dir: \"%s\"", tlsCacheDir))
		}

		configContent = strings.Join(updatedLines, "\n")
	} else {
		// Add HTTPS configuration at the end
		configContent = strings.TrimRight(configContent, "\n")
		if !strings.HasSuffix(configContent, "\n") && configContent != "" {
			configContent += "\n"
		}
		configContent += "enable_https: true\n"
		configContent += fmt.Sprintf("domain_name: \"%s\"\n", domain)
		configContent += fmt.Sprintf("tls_cache_dir: \"%s\"\n", tlsCacheDir)
	}

	// Write updated config
	if err := os.WriteFile(gatewayPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write gateway config: %w", err)
	}

	// Fix ownership
	exec.Command("chown", "debros:debros", gatewayPath).Run()

	return nil
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

func installOlric() {
	fmt.Printf("💾 Installing Olric cache server...\n")

	// Check if already installed
	if _, err := exec.LookPath("olric-server"); err == nil {
		fmt.Printf("   ✓ Olric already installed\n")
		configureFirewallForOlric()
		return
	}

	// Ensure Go is available (required for go install)
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Go not found - cannot install Olric. Please install Go first.\n")
		return
	}

	fmt.Printf("   Installing Olric server via go install...\n")
	cmd := exec.Command("go", "install", "github.com/olric-data/olric/cmd/olric-server@v0.7.0")
	cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin")
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to install Olric: %v\n", err)
		if len(output) > 0 {
			fmt.Fprintf(os.Stderr, "   Output: %s\n", string(output))
		}
		fmt.Fprintf(os.Stderr, "   You can manually install with: go install github.com/olric-data/olric/cmd/olric-server@v0.7.0\n")
		return
	}

	// Verify installation
	if _, err := exec.LookPath("olric-server"); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Olric installation verification failed: binary not found in PATH\n")
		fmt.Fprintf(os.Stderr, "   Make sure /usr/local/bin is in PATH\n")
		return
	}

	fmt.Printf("   ✓ Olric installed\n")

	// Configure firewall
	configureFirewallForOlric()

	// Create Olric config directory
	olricConfigDir := "/home/debros/.debros/olric"
	if err := os.MkdirAll(olricConfigDir, 0755); err == nil {
		configPath := olricConfigDir + "/config.yaml"
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			configContent := `server:
  bindAddr: "localhost"
  bindPort: 3320

memberlist:
  environment: local
  bindAddr: "localhost"
  bindPort: 3322

`
			if err := os.WriteFile(configPath, []byte(configContent), 0644); err == nil {
				exec.Command("chown", "debros:debros", configPath).Run()
				fmt.Printf("   ✓ Olric config created at %s\n", configPath)
			}
		}
		exec.Command("chown", "-R", "debros:debros", olricConfigDir).Run()
	}
}

func configureFirewallForOlric() {
	fmt.Printf("   Checking firewall configuration for Olric...\n")

	// Check for UFW
	if _, err := exec.LookPath("ufw"); err == nil {
		output, _ := exec.Command("ufw", "status").CombinedOutput()
		if strings.Contains(string(output), "Status: active") {
			fmt.Printf("   Adding UFW rules for Olric...\n")
			exec.Command("ufw", "allow", "3320/tcp", "comment", "Olric HTTP API").Run()
			exec.Command("ufw", "allow", "3322/tcp", "comment", "Olric Memberlist").Run()
			fmt.Printf("   ✓ UFW rules added for Olric\n")
			return
		}
	}

	// Check for firewalld
	if _, err := exec.LookPath("firewall-cmd"); err == nil {
		output, _ := exec.Command("firewall-cmd", "--state").CombinedOutput()
		if strings.Contains(string(output), "running") {
			fmt.Printf("   Adding firewalld rules for Olric...\n")
			exec.Command("firewall-cmd", "--permanent", "--add-port=3320/tcp").Run()
			exec.Command("firewall-cmd", "--permanent", "--add-port=3322/tcp").Run()
			exec.Command("firewall-cmd", "--reload").Run()
			fmt.Printf("   ✓ firewalld rules added for Olric\n")
			return
		}
	}

	// Check for iptables
	if _, err := exec.LookPath("iptables"); err == nil {
		output, _ := exec.Command("iptables", "-L", "-n").CombinedOutput()
		if strings.Contains(string(output), "Chain INPUT") {
			fmt.Printf("   Adding iptables rules for Olric...\n")
			exec.Command("iptables", "-A", "INPUT", "-p", "tcp", "--dport", "3320", "-j", "ACCEPT", "-m", "comment", "--comment", "Olric HTTP API").Run()
			exec.Command("iptables", "-A", "INPUT", "-p", "tcp", "--dport", "3322", "-j", "ACCEPT", "-m", "comment", "--comment", "Olric Memberlist").Run()

			// Try to save rules
			if _, err := exec.LookPath("netfilter-persistent"); err == nil {
				exec.Command("netfilter-persistent", "save").Run()
			} else if _, err := exec.LookPath("iptables-save"); err == nil {
				cmd := exec.Command("sh", "-c", "iptables-save > /etc/iptables/rules.v4")
				cmd.Run()
			}
			fmt.Printf("   ✓ iptables rules added for Olric\n")
			return
		}
	}

	fmt.Printf("   No active firewall detected for Olric\n")
}

func installIPFS() {
	fmt.Printf("🌐 Installing IPFS (Kubo) and IPFS Cluster...\n")

	// Check if IPFS is already installed
	if _, err := exec.LookPath("ipfs"); err == nil {
		fmt.Printf("   ✓ IPFS (Kubo) already installed\n")
	} else {
		fmt.Printf("   Installing IPFS (Kubo)...\n")
		// Install IPFS via official installation script
		cmd := exec.Command("bash", "-c", "curl -fsSL https://dist.ipfs.tech/kubo/v0.27.0/install.sh | bash")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to install IPFS: %v\n", err)
			fmt.Fprintf(os.Stderr, "   You may need to install IPFS manually: https://docs.ipfs.tech/install/command-line/\n")
			return
		}
		// Make sure ipfs is in PATH
		exec.Command("ln", "-sf", "/usr/local/bin/ipfs", "/usr/bin/ipfs").Run()
		fmt.Printf("   ✓ IPFS (Kubo) installed\n")
	}

	// Check if IPFS Cluster is already installed
	if _, err := exec.LookPath("ipfs-cluster-service"); err == nil {
		fmt.Printf("   ✓ IPFS Cluster already installed\n")
	} else {
		fmt.Printf("   Installing IPFS Cluster...\n")
		// Install IPFS Cluster via go install
		if _, err := exec.LookPath("go"); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Go not found - cannot install IPFS Cluster. Please install Go first.\n")
			return
		}
		cmd := exec.Command("go", "install", "github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-service@latest")
		cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin")
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to install IPFS Cluster: %v\n", err)
			if len(output) > 0 {
				fmt.Fprintf(os.Stderr, "   Output: %s\n", string(output))
			}
			fmt.Fprintf(os.Stderr, "   You can manually install with: go install github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-service@latest\n")
			return
		}
		// Also install ipfs-cluster-ctl for management
		exec.Command("go", "install", "github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-ctl@latest").Run()
		fmt.Printf("   ✓ IPFS Cluster installed\n")
	}

	// Configure firewall for IPFS and Cluster
	configureFirewallForIPFS()

	fmt.Printf("   ✓ IPFS and IPFS Cluster setup complete\n")
}

func configureFirewallForIPFS() {
	fmt.Printf("   Checking firewall configuration for IPFS...\n")

	// Check for UFW
	if _, err := exec.LookPath("ufw"); err == nil {
		output, _ := exec.Command("ufw", "status").CombinedOutput()
		if strings.Contains(string(output), "Status: active") {
			fmt.Printf("   Adding UFW rules for IPFS and Cluster...\n")
			exec.Command("ufw", "allow", "4001/tcp", "comment", "IPFS Swarm").Run()
			exec.Command("ufw", "allow", "5001/tcp", "comment", "IPFS API").Run()
			exec.Command("ufw", "allow", "9094/tcp", "comment", "IPFS Cluster API").Run()
			exec.Command("ufw", "allow", "9096/tcp", "comment", "IPFS Cluster Swarm").Run()
			fmt.Printf("   ✓ UFW rules added for IPFS\n")
			return
		}
	}

	// Check for firewalld
	if _, err := exec.LookPath("firewall-cmd"); err == nil {
		output, _ := exec.Command("firewall-cmd", "--state").CombinedOutput()
		if strings.Contains(string(output), "running") {
			fmt.Printf("   Adding firewalld rules for IPFS...\n")
			exec.Command("firewall-cmd", "--permanent", "--add-port=4001/tcp").Run()
			exec.Command("firewall-cmd", "--permanent", "--add-port=5001/tcp").Run()
			exec.Command("firewall-cmd", "--permanent", "--add-port=9094/tcp").Run()
			exec.Command("firewall-cmd", "--permanent", "--add-port=9096/tcp").Run()
			exec.Command("firewall-cmd", "--reload").Run()
			fmt.Printf("   ✓ firewalld rules added for IPFS\n")
			return
		}
	}

	fmt.Printf("   No active firewall detected for IPFS\n")
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

	// Remove existing repository if it exists (always start fresh)
	if _, err := os.Stat("/home/debros/src"); err == nil {
		fmt.Printf("   Removing existing repository...\n")
		// Remove as root since we're running as root
		if err := os.RemoveAll("/home/debros/src"); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to remove existing repo as root: %v\n", err)
			// Try as debros user as fallback (might work if files are owned by debros)
			removeCmd := exec.Command("sudo", "-u", "debros", "rm", "-rf", "/home/debros/src")
			if output, err := removeCmd.CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Failed to remove existing repo as debros user: %v\n%s\n", err, output)
			}
		}
		// Wait a moment to ensure filesystem syncs
		time.Sleep(100 * time.Millisecond)
	}

	// Ensure parent directory exists and has correct permissions
	if err := os.MkdirAll("/home/debros", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to ensure debros home directory exists: %v\n", err)
		os.Exit(1)
	}
	if err := exec.Command("chown", "debros:debros", "/home/debros").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to chown debros home directory: %v\n", err)
	}

	// Clone fresh repository
	fmt.Printf("   Cloning repository...\n")
	cmd := exec.Command("sudo", "-u", "debros", "git", "clone", "--branch", branch, "--depth", "1", "https://github.com/DeBrosOfficial/network.git", "/home/debros/src")
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to clone repo: %v\n%s\n", err, output)
		os.Exit(1)
	}

	// Build
	fmt.Printf("   Building binaries...\n")

	// Ensure Go is in PATH for the build
	os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/go/bin")

	// Use sudo with --preserve-env=PATH to pass Go path to debros user
	// Set HOME so Go knows where to create module cache
	cmd = exec.Command("sudo", "--preserve-env=PATH", "-u", "debros", "make", "build")
	cmd.Dir = "/home/debros/src"
	cmd.Env = append(os.Environ(), "HOME=/home/debros", "PATH="+os.Getenv("PATH")+":/usr/local/go/bin")
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to build: %v\n%s\n", err, output)
		os.Exit(1)
	}

	// Copy binaries
	copyCmd := exec.Command("sh", "-c", "cp -r /home/debros/src/bin/* /home/debros/bin/")
	if output, err := copyCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to copy binaries: %v\n%s\n", err, output)
		os.Exit(1)
	}

	chownCmd := exec.Command("chown", "-R", "debros:debros", "/home/debros/bin")
	if err := chownCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to chown binaries: %v\n", err)
	}

	chmodCmd := exec.Command("chmod", "-R", "755", "/home/debros/bin")
	if err := chmodCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to chmod binaries: %v\n", err)
	}

	fmt.Printf("   ✓ Built and installed\n")
}

func generateConfigsInteractive(force bool) {
	fmt.Printf("⚙️  Generating configurations...\n\n")

	nodeConfigPath := "/home/debros/.debros/node.yaml"
	gatewayPath := "/home/debros/.debros/gateway.yaml"

	// Check if configs already exist
	nodeExists := false
	gatewayExists := false
	if _, err := os.Stat(nodeConfigPath); err == nil {
		nodeExists = true
	}
	if _, err := os.Stat(gatewayPath); err == nil {
		gatewayExists = true
	}

	// If both configs exist and not forcing, skip configuration prompts
	if nodeExists && gatewayExists && !force {
		fmt.Printf("   ℹ️  Configuration files already exist (node.yaml and gateway.yaml)\n")
		fmt.Printf("   ℹ️  Skipping configuration generation\n\n")

		// Only offer to add HTTPS if not already enabled
		httpsAlreadyEnabled := false
		if data, err := os.ReadFile(gatewayPath); err == nil {
			httpsAlreadyEnabled = strings.Contains(string(data), "enable_https: true")
		}

		if !httpsAlreadyEnabled {
			fmt.Printf("🌐 Domain and HTTPS Configuration\n")
			fmt.Printf("Would you like to add HTTPS with a domain name to your existing gateway config? (yes/no) [default: no]: ")
			reader := bufio.NewReader(os.Stdin)
			addHTTPSResponse, _ := reader.ReadString('\n')
			addHTTPSResponse = strings.ToLower(strings.TrimSpace(addHTTPSResponse))

			if addHTTPSResponse == "yes" || addHTTPSResponse == "y" {
				// Get VPS IP for DNS verification
				vpsIP, err := getVPSIPv4Address()
				if err != nil {
					fmt.Fprintf(os.Stderr, "⚠️  Failed to detect IPv4 address: %v\n", err)
					fmt.Fprintf(os.Stderr, "   Using 0.0.0.0 as fallback\n")
					vpsIP = "0.0.0.0"
				}

				// Check if ports 80 and 443 are available
				portsAvailable, portIssues := checkPorts80And443()
				if !portsAvailable {
					fmt.Fprintf(os.Stderr, "\n⚠️  Cannot enable HTTPS: %s is already in use\n", portIssues)
					fmt.Fprintf(os.Stderr, "   You will need to configure HTTPS manually if you want to use a domain.\n\n")
				} else {
					// Prompt for domain and update existing config
					domain := promptDomainForHTTPS(reader, vpsIP)
					if domain != "" {
						// Update existing gateway config with HTTPS settings
						if err := updateGatewayConfigWithHTTPS(gatewayPath, domain); err != nil {
							fmt.Fprintf(os.Stderr, "⚠️  Failed to update gateway config with HTTPS: %v\n", err)
						} else {
							fmt.Printf("   ✓ HTTPS configuration added to existing gateway.yaml\n")
							// Create TLS cache directory
							tlsCacheDir := "/home/debros/.debros/tls-cache"
							if err := os.MkdirAll(tlsCacheDir, 0755); err == nil {
								exec.Command("chown", "-R", "debros:debros", tlsCacheDir).Run()
								fmt.Printf("   ✓ TLS cache directory created: %s\n", tlsCacheDir)
							}
						}
					}
				}
			}
		} else {
			fmt.Printf("   ℹ️  HTTPS is already enabled in gateway.yaml\n")
		}

		fmt.Printf("\n   ✓ Configurations ready\n")
		return
	}

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

	// Check if node.yaml already exists
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
			const defaultRQLiteHTTPPort = 5001
			var joinAddr string
			if bootstrapPeers != "" {
				firstPeer := strings.Split(bootstrapPeers, ",")[0]
				firstPeer = strings.TrimSpace(firstPeer)
				extractedIP := extractIPFromMultiaddr(firstPeer)
				if extractedIP != "" {
					joinAddr = fmt.Sprintf("%s:%d", extractedIP, defaultRQLiteHTTPPort)
				} else {
					joinAddr = fmt.Sprintf("localhost:%d", defaultRQLiteHTTPPort)
				}
			} else {
				joinAddr = fmt.Sprintf("localhost:%d", defaultRQLiteHTTPPort)
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

		// Initialize IPFS and Cluster for this node
		var nodeID string
		if isBootstrap {
			nodeID = "bootstrap"
		} else {
			nodeID = "node"
		}
		if err := initializeIPFSForNode(nodeID, vpsIP, isBootstrap); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to initialize IPFS/Cluster: %v\n", err)
			fmt.Fprintf(os.Stderr, "   You may need to initialize IPFS and Cluster manually\n")
		}

		// Generate Olric config file for this node (uses multicast discovery)
		var olricConfigPath string
		if isBootstrap {
			olricConfigPath = "/home/debros/.debros/bootstrap/olric-config.yaml"
		} else {
			olricConfigPath = "/home/debros/.debros/node/olric-config.yaml"
		}
		if err := generateOlricConfig(olricConfigPath, vpsIP, 3320, 3322); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to generate Olric config: %v\n", err)
		} else {
			fmt.Printf("   ✓ Olric config created: %s\n", olricConfigPath)
		}
	}

	// Generate gateway config
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
				// Prompt for domain name
				domain = promptDomainForHTTPS(reader, vpsIP)
				if domain != "" {
					enableHTTPS = true
					// Set TLS cache directory if HTTPS is enabled
					tlsCacheDir = "/home/debros/.debros/tls-cache"
					// Create TLS cache directory
					if err := os.MkdirAll(tlsCacheDir, 0755); err != nil {
						fmt.Fprintf(os.Stderr, "⚠️  Failed to create TLS cache directory: %v\n", err)
					} else {
						exec.Command("chown", "-R", "debros:debros", tlsCacheDir).Run()
						fmt.Printf("   ✓ TLS cache directory created: %s\n", tlsCacheDir)
					}
				} else {
					enableHTTPS = false
				}
			}
		}

		// For Olric servers, use localhost for local dev, or current node IP
		// In production, gateway will discover Olric nodes via LibP2P network
		var olricServers []string
		if bootstrapPeers == "" {
			// Local development - use localhost
			olricServers = []string{"localhost:3320"}
		} else {
			// Production - start with current node, will discover others via LibP2P
			olricServers = []string{fmt.Sprintf("%s:3320", vpsIP)}
		}

		// Gateway config should include bootstrap peers if this is a regular node
		// (bootstrap nodes don't need bootstrap peers since they are the bootstrap)
		gatewayConfig := generateGatewayConfigDirect(bootstrapPeers, enableHTTPS, domain, tlsCacheDir, olricServers)
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
  ipfs:
    # IPFS Cluster API endpoint for pin management (leave empty to disable)
    cluster_api_url: "http://localhost:9094"
    # IPFS HTTP API endpoint for content retrieval
    api_url: "http://localhost:5001"
    # Timeout for IPFS operations
    timeout: "60s"
    # Replication factor for pinned content
    replication_factor: 3
    # Enable client-side encryption before upload
    enable_encryption: true

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
		joinAddr = fmt.Sprintf("localhost:%d", rqliteHTTPPort)
	}

	// Generate Olric config file for regular node (uses multicast discovery)
	olricConfigPath := "/home/debros/.debros/node/olric-config.yaml"
	generateOlricConfig(olricConfigPath, ipAddr, 3320, 3322)

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
  ipfs:
    # IPFS Cluster API endpoint for pin management (leave empty to disable)
    cluster_api_url: "http://localhost:9094"
    # IPFS HTTP API endpoint for content retrieval
    api_url: "http://localhost:5001"
    # Timeout for IPFS operations
    timeout: "60s"
    # Replication factor for pinned content
    replication_factor: 3
    # Enable client-side encryption before upload
    enable_encryption: true

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
func generateGatewayConfigDirect(bootstrapPeers string, enableHTTPS bool, domain, tlsCacheDir string, olricServers []string) string {
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

	// Olric servers configuration
	var olricYAML strings.Builder
	if len(olricServers) > 0 {
		olricYAML.WriteString("olric_servers:\n")
		for _, server := range olricServers {
			fmt.Fprintf(&olricYAML, "  - \"%s\"\n", server)
		}
	} else {
		// Default to localhost for local development
		olricYAML.WriteString("olric_servers:\n")
		olricYAML.WriteString("  - \"localhost:3320\"\n")
	}

	// IPFS Cluster configuration
	ipfsYAML := `ipfs_cluster_api_url: "http://localhost:9094"
ipfs_api_url: "http://localhost:9105"
ipfs_timeout: "60s"
ipfs_replication_factor: 3
`

	return fmt.Sprintf(`listen_addr: ":6001"
client_namespace: "default"
rqlite_dsn: ""
%s
%s
%s
%s
`, peersYAML.String(), httpsYAML.String(), olricYAML.String(), ipfsYAML)
}

// generateOlricConfig generates an Olric configuration file
// Uses multicast discovery - peers will be discovered dynamically via LibP2P network
func generateOlricConfig(configPath, bindIP string, httpPort, memberlistPort int) error {
	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create Olric config directory: %w", err)
	}

	var config strings.Builder
	config.WriteString("server:\n")
	config.WriteString(fmt.Sprintf("  bindAddr: \"%s\"\n", bindIP))
	config.WriteString(fmt.Sprintf("  bindPort: %d\n", httpPort))
	config.WriteString("\n")
	config.WriteString("memberlist:\n")
	config.WriteString("  environment: local\n")
	config.WriteString(fmt.Sprintf("  bindAddr: \"%s\"\n", bindIP))
	config.WriteString(fmt.Sprintf("  bindPort: %d\n", memberlistPort))
	config.WriteString("\n")

	// Write config file
	if err := os.WriteFile(configPath, []byte(config.String()), 0644); err != nil {
		return fmt.Errorf("failed to write Olric config: %w", err)
	}

	// Fix ownership
	exec.Command("chown", "debros:debros", configPath).Run()
	return nil
}

// getOrGenerateClusterSecret gets or generates a shared cluster secret
func getOrGenerateClusterSecret() (string, error) {
	secretPath := "/home/debros/.debros/cluster-secret"

	// Try to read existing secret
	if data, err := os.ReadFile(secretPath); err == nil {
		secret := strings.TrimSpace(string(data))
		if len(secret) == 64 {
			return secret, nil
		}
	}

	// Generate new secret (64 hex characters = 32 bytes)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate cluster secret: %w", err)
	}
	secret := hex.EncodeToString(bytes)

	// Save secret
	if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
		return "", fmt.Errorf("failed to save cluster secret: %w", err)
	}
	exec.Command("chown", "debros:debros", secretPath).Run()

	return secret, nil
}

// getOrGenerateSwarmKey gets or generates a shared IPFS swarm key
// Returns the swarm key content as bytes (formatted for IPFS)
func getOrGenerateSwarmKey() ([]byte, error) {
	secretPath := "/home/debros/.debros/swarm.key"

	// Try to read existing key
	if data, err := os.ReadFile(secretPath); err == nil {
		// Validate it's a proper swarm key format
		content := string(data)
		if strings.Contains(content, "/key/swarm/psk/1.0.0/") {
			return data, nil
		}
	}

	// Generate new key (32 bytes)
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("failed to generate swarm key: %w", err)
	}

	// Format as IPFS swarm key file
	keyHex := strings.ToUpper(hex.EncodeToString(keyBytes))
	content := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", keyHex)

	// Save key
	if err := os.WriteFile(secretPath, []byte(content), 0600); err != nil {
		return nil, fmt.Errorf("failed to save swarm key: %w", err)
	}
	exec.Command("chown", "debros:debros", secretPath).Run()

	fmt.Printf("   ✓ Generated private swarm key\n")
	return []byte(content), nil
}

// ensureSwarmKey ensures the swarm key exists in the IPFS repo
func ensureSwarmKey(repoPath string, swarmKey []byte) error {
	swarmKeyPath := filepath.Join(repoPath, "swarm.key")

	// Check if swarm key already exists
	if _, err := os.Stat(swarmKeyPath); err == nil {
		// Verify it matches (optional: could compare content)
		return nil
	}

	// Create swarm key file in repo
	if err := os.WriteFile(swarmKeyPath, swarmKey, 0600); err != nil {
		return fmt.Errorf("failed to write swarm key to repo: %w", err)
	}

	// Fix ownership
	exec.Command("chown", "debros:debros", swarmKeyPath).Run()
	return nil
}

// initializeIPFSForNode initializes IPFS and IPFS Cluster for a node
func initializeIPFSForNode(nodeID, vpsIP string, isBootstrap bool) error {
	fmt.Printf("   Initializing IPFS and Cluster for node %s...\n", nodeID)

	// Get or generate cluster secret
	secret, err := getOrGenerateClusterSecret()
	if err != nil {
		return fmt.Errorf("failed to get cluster secret: %w", err)
	}

	// Get or generate swarm key for private network
	swarmKey, err := getOrGenerateSwarmKey()
	if err != nil {
		return fmt.Errorf("failed to get swarm key: %w", err)
	}

	// Determine data directories
	var ipfsDataDir, clusterDataDir string
	if nodeID == "bootstrap" {
		ipfsDataDir = "/home/debros/.debros/bootstrap/ipfs"
		clusterDataDir = "/home/debros/.debros/bootstrap/ipfs-cluster"
	} else {
		ipfsDataDir = "/home/debros/.debros/node/ipfs"
		clusterDataDir = "/home/debros/.debros/node/ipfs-cluster"
	}

	// Create directories
	os.MkdirAll(ipfsDataDir, 0755)
	os.MkdirAll(clusterDataDir, 0755)
	exec.Command("chown", "-R", "debros:debros", ipfsDataDir).Run()
	exec.Command("chown", "-R", "debros:debros", clusterDataDir).Run()

	// Initialize IPFS if not already initialized
	ipfsRepoPath := filepath.Join(ipfsDataDir, "repo")
	if _, err := os.Stat(filepath.Join(ipfsRepoPath, "config")); os.IsNotExist(err) {
		fmt.Printf("      Initializing IPFS repository...\n")
		cmd := exec.Command("sudo", "-u", "debros", "ipfs", "init", "--profile=server", "--repo-dir="+ipfsRepoPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to initialize IPFS: %v\n%s", err, string(output))
		}

		// Ensure swarm key is in place (creates private network)
		if err := ensureSwarmKey(ipfsRepoPath, swarmKey); err != nil {
			return fmt.Errorf("failed to set swarm key: %w", err)
		}

		// Configure IPFS API and Gateway addresses
		exec.Command("sudo", "-u", "debros", "ipfs", "config", "--json", "Addresses.API", `["/ip4/localhost/tcp/5001"]`, "--repo-dir="+ipfsRepoPath).Run()
		exec.Command("sudo", "-u", "debros", "ipfs", "config", "--json", "Addresses.Gateway", `["/ip4/localhost/tcp/8080"]`, "--repo-dir="+ipfsRepoPath).Run()
		exec.Command("sudo", "-u", "debros", "ipfs", "config", "--json", "Addresses.Swarm", `["/ip4/0.0.0.0/tcp/4001","/ip6/::/tcp/4001"]`, "--repo-dir="+ipfsRepoPath).Run()
		fmt.Printf("      ✓ IPFS initialized with private swarm key\n")
	} else {
		// Repo exists, but ensure swarm key is present
		if err := ensureSwarmKey(ipfsRepoPath, swarmKey); err != nil {
			return fmt.Errorf("failed to set swarm key: %w", err)
		}
		fmt.Printf("      ✓ IPFS repository already exists, swarm key ensured\n")
	}

	// Initialize IPFS Cluster if not already initialized
	clusterConfigPath := filepath.Join(clusterDataDir, "service.json")
	if _, err := os.Stat(clusterConfigPath); os.IsNotExist(err) {
		fmt.Printf("      Initializing IPFS Cluster...\n")

		// Generate cluster config
		clusterConfig := generateClusterServiceConfig(nodeID, vpsIP, secret, isBootstrap)

		// Write config
		configJSON, err := json.MarshalIndent(clusterConfig, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal cluster config: %w", err)
		}

		if err := os.WriteFile(clusterConfigPath, configJSON, 0644); err != nil {
			return fmt.Errorf("failed to write cluster config: %w", err)
		}
		exec.Command("chown", "debros:debros", clusterConfigPath).Run()

		fmt.Printf("      ✓ IPFS Cluster initialized\n")
	}

	return nil
}

// getClusterPeerID gets the cluster peer ID from a running cluster service
func getClusterPeerID(clusterAPIURL string) (string, error) {
	cmd := exec.Command("ipfs-cluster-ctl", "--host", clusterAPIURL, "id")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get cluster peer ID: %v\n%s", err, string(output))
	}

	// Parse output to extract peer ID
	// Output format: "12D3KooW..."
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "12D3Koo") {
			return line, nil
		}
	}

	return "", fmt.Errorf("could not parse cluster peer ID from output: %s", string(output))
}

// getClusterPeerMultiaddr constructs the cluster peer multiaddr
func getClusterPeerMultiaddr(vpsIP, peerID string) string {
	return fmt.Sprintf("/ip4/%s/tcp/9096/p2p/%s", vpsIP, peerID)
}

// clusterServiceConfig represents IPFS Cluster service.json structure
type clusterServiceConfig struct {
	Cluster       clusterConfig       `json:"cluster"`
	Consensus     consensusConfig     `json:"consensus"`
	API           apiConfig           `json:"api"`
	IPFSConnector ipfsConnectorConfig `json:"ipfs_connector"`
	Datastore     datastoreConfig     `json:"datastore"`
}

type clusterConfig struct {
	ID                string                  `json:"id"`
	PrivateKey        string                  `json:"private_key"`
	Secret            string                  `json:"secret"`
	Peername          string                  `json:"peername"`
	Bootstrap         []string                `json:"bootstrap"`
	LeaveOnShutdown   bool                    `json:"leave_on_shutdown"`
	ListenMultiaddr   string                  `json:"listen_multiaddress"`
	ConnectionManager connectionManagerConfig `json:"connection_manager"`
}

type connectionManagerConfig struct {
	LowWater    int    `json:"low_water"`
	HighWater   int    `json:"high_water"`
	GracePeriod string `json:"grace_period"`
}

type consensusConfig struct {
	CRDT crdtConfig `json:"crdt"`
}

type crdtConfig struct {
	ClusterName  string   `json:"cluster_name"`
	TrustedPeers []string `json:"trusted_peers"`
}

type apiConfig struct {
	RestAPI restAPIConfig `json:"restapi"`
}

type restAPIConfig struct {
	HTTPListenMultiaddress string      `json:"http_listen_multiaddress"`
	ID                     string      `json:"id"`
	BasicAuthCredentials   interface{} `json:"basic_auth_credentials"`
}

type ipfsConnectorConfig struct {
	IPFSHTTP ipfsHTTPConfig `json:"ipfshttp"`
}

type ipfsHTTPConfig struct {
	NodeMultiaddress string `json:"node_multiaddress"`
}

type datastoreConfig struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

// generateClusterServiceConfig generates IPFS Cluster service.json config
func generateClusterServiceConfig(nodeID, vpsIP, secret string, isBootstrap bool) clusterServiceConfig {
	clusterListenAddr := "/ip4/0.0.0.0/tcp/9096"
	restAPIListenAddr := "/ip4/0.0.0.0/tcp/9094"

	// For bootstrap node, use empty bootstrap list
	// For other nodes, bootstrap list will be set when starting the service
	bootstrap := []string{}

	return clusterServiceConfig{
		Cluster: clusterConfig{
			Peername:        nodeID,
			Secret:          secret,
			Bootstrap:       bootstrap,
			LeaveOnShutdown: false,
			ListenMultiaddr: clusterListenAddr,
			ConnectionManager: connectionManagerConfig{
				LowWater:    50,
				HighWater:   200,
				GracePeriod: "20s",
			},
		},
		Consensus: consensusConfig{
			CRDT: crdtConfig{
				ClusterName:  "debros-cluster",
				TrustedPeers: []string{"*"}, // Trust all peers
			},
		},
		API: apiConfig{
			RestAPI: restAPIConfig{
				HTTPListenMultiaddress: restAPIListenAddr,
				ID:                     "",
				BasicAuthCredentials:   nil,
			},
		},
		IPFSConnector: ipfsConnectorConfig{
			IPFSHTTP: ipfsHTTPConfig{
				NodeMultiaddress: "/ip4/localhost/tcp/5001",
			},
		},
		Datastore: datastoreConfig{
			Type: "badger",
			Path: fmt.Sprintf("/home/debros/.debros/%s/ipfs-cluster/badger", nodeID),
		},
	}
}

func createSystemdServices() {
	fmt.Printf("🔧 Creating systemd services...\n")

	// IPFS service (runs on all nodes)
	ipfsService := `[Unit]
Description=IPFS Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
Environment=HOME=/home/debros
ExecStartPre=/bin/bash -c 'if [ -f /home/debros/.debros/node.yaml ]; then export IPFS_PATH=/home/debros/.debros/node/ipfs/repo; elif [ -f /home/debros/.debros/bootstrap.yaml ]; then export IPFS_PATH=/home/debros/.debros/bootstrap/ipfs/repo; else export IPFS_PATH=/home/debros/.debros/bootstrap/ipfs/repo; fi'
ExecStartPre=/bin/bash -c 'if [ -f /home/debros/.debros/swarm.key ] && [ ! -f ${IPFS_PATH}/swarm.key ]; then cp /home/debros/.debros/swarm.key ${IPFS_PATH}/swarm.key && chmod 600 ${IPFS_PATH}/swarm.key; fi'
ExecStart=/usr/bin/ipfs daemon --enable-pubsub-experiment --repo-dir=${IPFS_PATH}
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=ipfs

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/home/debros

[Install]
WantedBy=multi-user.target
`

	if err := os.WriteFile("/etc/systemd/system/debros-ipfs.service", []byte(ipfsService), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to create IPFS service: %v\n", err)
		os.Exit(1)
	}

	// IPFS Cluster service (runs on all nodes)
	clusterService := `[Unit]
Description=IPFS Cluster Service
After=debros-ipfs.service
Wants=debros-ipfs.service
Requires=debros-ipfs.service

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=/home/debros
Environment=HOME=/home/debros
ExecStartPre=/bin/bash -c 'if [ -f /home/debros/.debros/node.yaml ]; then export CLUSTER_PATH=/home/debros/.debros/node/ipfs-cluster; elif [ -f /home/debros/.debros/bootstrap.yaml ]; then export CLUSTER_PATH=/home/debros/.debros/bootstrap/ipfs-cluster; else export CLUSTER_PATH=/home/debros/.debros/bootstrap/ipfs-cluster; fi'
ExecStart=/usr/local/bin/ipfs-cluster-service daemon --config ${CLUSTER_PATH}/service.json
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=ipfs-cluster

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/home/debros

[Install]
WantedBy=multi-user.target
`

	if err := os.WriteFile("/etc/systemd/system/debros-ipfs-cluster.service", []byte(clusterService), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to create IPFS Cluster service: %v\n", err)
		os.Exit(1)
	}

	// Node service
	nodeService := `[Unit]
Description=DeBros Network Node
After=network-online.target debros-ipfs-cluster.service
Wants=network-online.target debros-ipfs-cluster.service
Requires=debros-ipfs-cluster.service

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
	exec.Command("systemctl", "enable", "debros-ipfs").Run()
	exec.Command("systemctl", "enable", "debros-ipfs-cluster").Run()
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

	// Start IPFS first (required by Cluster)
	startOrRestartService("debros-ipfs")

	// Wait a bit for IPFS to start
	time.Sleep(2 * time.Second)

	// Start IPFS Cluster (required by Node)
	startOrRestartService("debros-ipfs-cluster")

	// Wait a bit for Cluster to start
	time.Sleep(2 * time.Second)

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
