package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/environments/production"
	"github.com/DeBrosOfficial/network/pkg/installer"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	"github.com/multiformats/go-multiaddr"
)

// runInteractiveInstaller launches the TUI installer
func runInteractiveInstaller() {
	config, err := installer.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Convert TUI config to install args and run installation
	var args []string
	args = append(args, "--vps-ip", config.VpsIP)
	args = append(args, "--domain", config.Domain)
	args = append(args, "--branch", config.Branch)

	if config.NoPull {
		args = append(args, "--no-pull")
	}

	if !config.IsFirstNode {
		if config.JoinAddress != "" {
			args = append(args, "--join", config.JoinAddress)
		}
		if config.ClusterSecret != "" {
			args = append(args, "--cluster-secret", config.ClusterSecret)
		}
		if len(config.Peers) > 0 {
			args = append(args, "--peers", strings.Join(config.Peers, ","))
		}
	}

	// Re-run with collected args
	handleProdInstall(args)
}

// showDryRunSummary displays what would be done during installation without making changes
func showDryRunSummary(vpsIP, domain, branch string, peers []string, joinAddress string, isFirstNode bool, oramaDir string) {
	fmt.Printf("\n" + strings.Repeat("=", 70) + "\n")
	fmt.Printf("DRY RUN - No changes will be made\n")
	fmt.Printf(strings.Repeat("=", 70) + "\n\n")

	fmt.Printf("📋 Installation Summary:\n")
	fmt.Printf("  VPS IP:        %s\n", vpsIP)
	fmt.Printf("  Domain:        %s\n", domain)
	fmt.Printf("  Branch:        %s\n", branch)
	if isFirstNode {
		fmt.Printf("  Node Type:     First node (creates new cluster)\n")
	} else {
		fmt.Printf("  Node Type:     Joining existing cluster\n")
		if joinAddress != "" {
			fmt.Printf("  Join Address:  %s\n", joinAddress)
		}
		if len(peers) > 0 {
			fmt.Printf("  Peers:         %d peer(s)\n", len(peers))
			for _, peer := range peers {
				fmt.Printf("                 - %s\n", peer)
			}
		}
	}

	fmt.Printf("\n📁 Directories that would be created:\n")
	fmt.Printf("  %s/configs/\n", oramaDir)
	fmt.Printf("  %s/secrets/\n", oramaDir)
	fmt.Printf("  %s/data/ipfs/repo/\n", oramaDir)
	fmt.Printf("  %s/data/ipfs-cluster/\n", oramaDir)
	fmt.Printf("  %s/data/rqlite/\n", oramaDir)
	fmt.Printf("  %s/logs/\n", oramaDir)
	fmt.Printf("  %s/tls-cache/\n", oramaDir)

	fmt.Printf("\n🔧 Binaries that would be installed:\n")
	fmt.Printf("  - Go (if not present)\n")
	fmt.Printf("  - RQLite 8.43.0\n")
	fmt.Printf("  - IPFS/Kubo 0.38.2\n")
	fmt.Printf("  - IPFS Cluster (latest)\n")
	fmt.Printf("  - Olric 0.7.0\n")
	fmt.Printf("  - anyone-client (npm)\n")
	fmt.Printf("  - DeBros binaries (built from %s branch)\n", branch)

	fmt.Printf("\n🔐 Secrets that would be generated:\n")
	fmt.Printf("  - Cluster secret (64-hex)\n")
	fmt.Printf("  - IPFS swarm key\n")
	fmt.Printf("  - Node identity (Ed25519 keypair)\n")

	fmt.Printf("\n📝 Configuration files that would be created:\n")
	fmt.Printf("  - %s/configs/node.yaml\n", oramaDir)
	fmt.Printf("  - %s/configs/olric/config.yaml\n", oramaDir)

	fmt.Printf("\n⚙️  Systemd services that would be created:\n")
	fmt.Printf("  - debros-ipfs.service\n")
	fmt.Printf("  - debros-ipfs-cluster.service\n")
	fmt.Printf("  - debros-olric.service\n")
	fmt.Printf("  - debros-node.service (includes embedded gateway + RQLite)\n")
	fmt.Printf("  - debros-anyone-client.service\n")

	fmt.Printf("\n🌐 Ports that would be used:\n")
	fmt.Printf("  External (must be open in firewall):\n")
	fmt.Printf("    - 80   (HTTP for ACME/Let's Encrypt)\n")
	fmt.Printf("    - 443  (HTTPS gateway)\n")
	fmt.Printf("    - 4101 (IPFS swarm)\n")
	fmt.Printf("    - 7001 (RQLite Raft)\n")
	fmt.Printf("  Internal (localhost only):\n")
	fmt.Printf("    - 4501 (IPFS API)\n")
	fmt.Printf("    - 5001 (RQLite HTTP)\n")
	fmt.Printf("    - 6001 (Unified gateway)\n")
	fmt.Printf("    - 8080 (IPFS gateway)\n")
	fmt.Printf("    - 9050 (Anyone SOCKS5)\n")
	fmt.Printf("    - 9094 (IPFS Cluster API)\n")
	fmt.Printf("    - 3320/3322 (Olric)\n")

	fmt.Printf("\n" + strings.Repeat("=", 70) + "\n")
	fmt.Printf("To proceed with installation, run without --dry-run\n")
	fmt.Printf(strings.Repeat("=", 70) + "\n\n")
}

// validateGeneratedConfig loads and validates the generated node configuration
func validateGeneratedConfig(oramaDir string) error {
	configPath := filepath.Join(oramaDir, "configs", "node.yaml")

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("configuration file not found at %s", configPath)
	}

	// Load the config file
	file, err := os.Open(configPath)
	if err != nil {
		return fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var cfg config.Config
	if err := config.DecodeStrict(file, &cfg); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// Validate the configuration
	if errs := cfg.Validate(); len(errs) > 0 {
		var errMsgs []string
		for _, e := range errs {
			errMsgs = append(errMsgs, e.Error())
		}
		return fmt.Errorf("configuration validation errors:\n  - %s", strings.Join(errMsgs, "\n  - "))
	}

	return nil
}

// validateDNSRecord validates that the domain points to the expected IP address
// Returns nil if DNS is valid, warning message if DNS doesn't match but continues,
// or error if DNS lookup fails completely
func validateDNSRecord(domain, expectedIP string) error {
	if domain == "" {
		return nil // No domain provided, skip validation
	}

	ips, err := net.LookupIP(domain)
	if err != nil {
		// DNS lookup failed - this is a warning, not a fatal error
		// The user might be setting up DNS after installation
		fmt.Printf("  ⚠️  DNS lookup failed for %s: %v\n", domain, err)
		fmt.Printf("     Make sure DNS is configured before enabling HTTPS\n")
		return nil
	}

	// Check if any resolved IP matches the expected IP
	for _, ip := range ips {
		if ip.String() == expectedIP {
			fmt.Printf("  ✓ DNS validated: %s → %s\n", domain, expectedIP)
			return nil
		}
	}

	// DNS doesn't point to expected IP - warn but continue
	resolvedIPs := make([]string, len(ips))
	for i, ip := range ips {
		resolvedIPs[i] = ip.String()
	}
	fmt.Printf("  ⚠️  DNS mismatch: %s resolves to %v, expected %s\n", domain, resolvedIPs, expectedIP)
	fmt.Printf("     HTTPS certificate generation may fail until DNS is updated\n")
	return nil
}

// normalizePeers normalizes and validates peer multiaddrs
func normalizePeers(peersStr string) ([]string, error) {
	if peersStr == "" {
		return nil, nil
	}

	// Split by comma and trim whitespace
	rawPeers := strings.Split(peersStr, ",")
	peers := make([]string, 0, len(rawPeers))
	seen := make(map[string]bool)

	for _, peer := range rawPeers {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			continue
		}

		// Validate multiaddr format
		if _, err := multiaddr.NewMultiaddr(peer); err != nil {
			return nil, fmt.Errorf("invalid multiaddr %q: %w", peer, err)
		}

		// Deduplicate
		if !seen[peer] {
			peers = append(peers, peer)
			seen[peer] = true
		}
	}

	return peers, nil
}

// HandleProdCommand handles production environment commands
func HandleProdCommand(args []string) {
	if len(args) == 0 {
		showProdHelp()
		return
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "install":
		handleProdInstall(subargs)
	case "upgrade":
		handleProdUpgrade(subargs)
	case "migrate":
		handleProdMigrate(subargs)
	case "status":
		handleProdStatus()
	case "start":
		handleProdStart()
	case "stop":
		handleProdStop()
	case "restart":
		handleProdRestart()
	case "logs":
		handleProdLogs(subargs)
	case "uninstall":
		handleProdUninstall()
	case "help":
		showProdHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown prod subcommand: %s\n", subcommand)
		showProdHelp()
		os.Exit(1)
	}
}

func showProdHelp() {
	fmt.Printf("Production Environment Commands\n\n")
	fmt.Printf("Usage: orama <subcommand> [options]\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  install                   - Install production node (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --interactive         - Launch interactive TUI wizard\n")
	fmt.Printf("      --force               - Reconfigure all settings\n")
	fmt.Printf("      --vps-ip IP           - VPS public IP address (required)\n")
	fmt.Printf("      --domain DOMAIN       - Domain for this node (e.g., node-1.orama.network)\n")
	fmt.Printf("      --peers ADDRS         - Comma-separated peer multiaddrs (for joining cluster)\n")
	fmt.Printf("      --join ADDR           - RQLite join address IP:port (for joining cluster)\n")
	fmt.Printf("      --cluster-secret HEX  - 64-hex cluster secret (required when joining)\n")
	fmt.Printf("      --branch BRANCH       - Git branch to use (main or nightly, default: main)\n")
	fmt.Printf("      --no-pull             - Skip git clone/pull, use existing /home/debros/src\n")
	fmt.Printf("      --ignore-resource-checks - Skip disk/RAM/CPU prerequisite validation\n")
	fmt.Printf("      --dry-run             - Show what would be done without making changes\n")
	fmt.Printf("  upgrade                   - Upgrade existing installation (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --restart              - Automatically restart services after upgrade\n")
	fmt.Printf("      --branch BRANCH        - Git branch to use (main or nightly)\n")
	fmt.Printf("      --no-pull              - Skip git clone/pull, use existing source\n")
	fmt.Printf("  migrate                   - Migrate from old unified setup (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --dry-run              - Show what would be migrated without making changes\n")
	fmt.Printf("  status                    - Show status of production services\n")
	fmt.Printf("  start                     - Start all production services (requires root/sudo)\n")
	fmt.Printf("  stop                      - Stop all production services (requires root/sudo)\n")
	fmt.Printf("  restart                   - Restart all production services (requires root/sudo)\n")
	fmt.Printf("  logs <service>            - View production service logs\n")
	fmt.Printf("    Service aliases: node, ipfs, cluster, gateway, olric\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --follow              - Follow logs in real-time\n")
	fmt.Printf("  uninstall                 - Remove production services (requires root/sudo)\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  # First node (creates new cluster)\n")
	fmt.Printf("  sudo orama install --vps-ip 203.0.113.1 --domain node-1.orama.network\n\n")
	fmt.Printf("  # Join existing cluster\n")
	fmt.Printf("  sudo orama install --vps-ip 203.0.113.2 --domain node-2.orama.network \\\n")
	fmt.Printf("    --peers /ip4/203.0.113.1/tcp/4001/p2p/12D3KooW... \\\n")
	fmt.Printf("    --cluster-secret <64-hex-secret>\n\n")
	fmt.Printf("  # Upgrade\n")
	fmt.Printf("  sudo orama upgrade --restart\n\n")
	fmt.Printf("  # Service management\n")
	fmt.Printf("  sudo orama start\n")
	fmt.Printf("  sudo orama stop\n")
	fmt.Printf("  sudo orama restart\n\n")
	fmt.Printf("  orama status\n")
	fmt.Printf("  orama logs node --follow\n")
}

func handleProdInstall(args []string) {
	// Parse arguments using flag.FlagSet
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	force := fs.Bool("force", false, "Reconfigure all settings")
	skipResourceChecks := fs.Bool("ignore-resource-checks", false, "Skip disk/RAM/CPU prerequisite validation")
	vpsIP := fs.String("vps-ip", "", "VPS public IP address")
	domain := fs.String("domain", "", "Domain for this node (e.g., node-123.orama.network)")
	peersStr := fs.String("peers", "", "Comma-separated peer multiaddrs to connect to")
	joinAddress := fs.String("join", "", "RQLite join address (IP:port) to join existing cluster")
	branch := fs.String("branch", "main", "Git branch to use (main or nightly)")
	clusterSecret := fs.String("cluster-secret", "", "Hex-encoded 32-byte cluster secret (for joining existing cluster)")
	interactive := fs.Bool("interactive", false, "Run interactive TUI installer")
	dryRun := fs.Bool("dry-run", false, "Show what would be done without making changes")
	noPull := fs.Bool("no-pull", false, "Skip git clone/pull, use existing /home/debros/src")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "❌ Failed to parse flags: %v\n", err)
		os.Exit(1)
	}

	// Launch TUI installer if --interactive flag or no required args provided
	if *interactive || (*vpsIP == "" && len(args) == 0) {
		runInteractiveInstaller()
		return
	}

	// Validate branch
	if *branch != "main" && *branch != "nightly" {
		fmt.Fprintf(os.Stderr, "❌ Invalid branch: %s (must be 'main' or 'nightly')\n", *branch)
		os.Exit(1)
	}

	// Normalize and validate peers
	peers, err := normalizePeers(*peersStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Invalid peers: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Example: --peers /ip4/10.0.0.1/tcp/4001/p2p/Qm...,/ip4/10.0.0.2/tcp/4001/p2p/Qm...\n")
		os.Exit(1)
	}

	// Validate setup requirements
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production install must be run as root (use sudo)\n")
		os.Exit(1)
	}

	// Validate VPS IP is provided
		if *vpsIP == "" {
		fmt.Fprintf(os.Stderr, "❌ --vps-ip is required\n")
		fmt.Fprintf(os.Stderr, "   Usage: sudo orama install --vps-ip <public_ip>\n")
		fmt.Fprintf(os.Stderr, "   Or run: sudo orama install --interactive\n")
			os.Exit(1)
		}

	// Determine if this is the first node (creates new cluster) or joining existing cluster
	isFirstNode := len(peers) == 0 && *joinAddress == ""
	if isFirstNode {
		fmt.Printf("ℹ️  First node detected - will create new cluster\n")
	} else {
		fmt.Printf("ℹ️  Joining existing cluster\n")
		// Cluster secret is required when joining
		if *clusterSecret == "" {
			fmt.Fprintf(os.Stderr, "❌ --cluster-secret is required when joining an existing cluster\n")
			fmt.Fprintf(os.Stderr, "   Provide the 64-hex secret from an existing node (cat ~/.orama/secrets/cluster-secret)\n")
			os.Exit(1)
		}
		if err := production.ValidateClusterSecret(*clusterSecret); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Invalid --cluster-secret: %v\n", err)
			os.Exit(1)
		}
	}

	oramaHome := "/home/debros"
	oramaDir := oramaHome + "/.orama"

	// If cluster secret was provided, save it to secrets directory before setup
	if *clusterSecret != "" {
		secretsDir := filepath.Join(oramaDir, "secrets")
		if err := os.MkdirAll(secretsDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to create secrets directory: %v\n", err)
			os.Exit(1)
		}
		secretPath := filepath.Join(secretsDir, "cluster-secret")
		if err := os.WriteFile(secretPath, []byte(*clusterSecret), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to save cluster secret: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  ✓ Cluster secret saved\n")
	}

	setup := production.NewProductionSetup(oramaHome, os.Stdout, *force, *branch, *noPull, *skipResourceChecks)

	// Inform user if skipping git pull
	if *noPull {
		fmt.Printf("  ⚠️  --no-pull flag enabled: Skipping git clone/pull\n")
		fmt.Printf("     Using existing repository at /home/debros/src\n")
	}

	// Check port availability before proceeding
	if err := ensurePortsAvailable("install", defaultPorts()); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Validate DNS if domain is provided
	if *domain != "" {
		fmt.Printf("\n🌐 Pre-flight DNS validation...\n")
		validateDNSRecord(*domain, *vpsIP)
	}

	// Dry-run mode: show what would be done and exit
	if *dryRun {
		showDryRunSummary(*vpsIP, *domain, *branch, peers, *joinAddress, isFirstNode, oramaDir)
		return
	}

	// Save branch preference for future upgrades
	if err := production.SaveBranchPreference(oramaDir, *branch); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to save branch preference: %v\n", err)
	}

	// Phase 1: Check prerequisites
	fmt.Printf("\n📋 Phase 1: Checking prerequisites...\n")
	if err := setup.Phase1CheckPrerequisites(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Prerequisites check failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 2: Provision environment
	fmt.Printf("\n🛠️  Phase 2: Provisioning environment...\n")
	if err := setup.Phase2ProvisionEnvironment(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Environment provisioning failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 2b: Install binaries
	fmt.Printf("\nPhase 2b: Installing binaries...\n")
	if err := setup.Phase2bInstallBinaries(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Binary installation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 3: Generate secrets FIRST (before service initialization)
	// This ensures cluster secret and swarm key exist before repos are seeded
	fmt.Printf("\n🔐 Phase 3: Generating secrets...\n")
	if err := setup.Phase3GenerateSecrets(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Secret generation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 2c: Initialize services (after secrets are in place)
	fmt.Printf("\nPhase 2c: Initializing services...\n")
	if err := setup.Phase2cInitializeServices(peers, *vpsIP); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service initialization failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 4: Generate configs
	fmt.Printf("\n⚙️  Phase 4: Generating configurations...\n")
	enableHTTPS := *domain != ""
	if err := setup.Phase4GenerateConfigs(peers, *vpsIP, enableHTTPS, *domain, *joinAddress); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Configuration generation failed: %v\n", err)
		os.Exit(1)
	}

	// Validate generated configuration
	fmt.Printf("  Validating generated configuration...\n")
	if err := validateGeneratedConfig(oramaDir); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Configuration validation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ Configuration validated\n")

	// Phase 5: Create systemd services
	fmt.Printf("\n🔧 Phase 5: Creating systemd services...\n")
	if err := setup.Phase5CreateSystemdServices(enableHTTPS); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service creation failed: %v\n", err)
		os.Exit(1)
	}

	// Verify all services are running correctly with exponential backoff retries
	fmt.Printf("\n⏳ Verifying services are healthy...\n")
	if err := verifyProductionRuntimeWithRetry("prod install", 5, 3*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		fmt.Fprintf(os.Stderr, "   Installation completed but services are not healthy. Check logs with: orama logs <service>\n")
		os.Exit(1)
	}

	// Log completion with actual peer ID
	setup.LogSetupComplete(setup.NodePeerID)
	fmt.Printf("✅ Production installation complete and healthy!\n\n")
}

func handleProdUpgrade(args []string) {
	// Parse arguments using flag.FlagSet
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	force := fs.Bool("force", false, "Reconfigure all settings")
	restartServices := fs.Bool("restart", false, "Automatically restart services after upgrade")
	noPull := fs.Bool("no-pull", false, "Skip git clone/pull, use existing /home/debros/src")
	branch := fs.String("branch", "", "Git branch to use (main or nightly, uses saved preference if not specified)")

	// Support legacy flags for backwards compatibility
	fs.Bool("nightly", false, "Use nightly branch (deprecated, use --branch nightly)")
	fs.Bool("main", false, "Use main branch (deprecated, use --branch main)")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "❌ Failed to parse flags: %v\n", err)
		os.Exit(1)
	}

	// Handle legacy flags
	nightlyFlag := fs.Lookup("nightly")
	mainFlag := fs.Lookup("main")
	if nightlyFlag != nil && nightlyFlag.Value.String() == "true" {
		*branch = "nightly"
	}
	if mainFlag != nil && mainFlag.Value.String() == "true" {
		*branch = "main"
	}

	// Validate branch if provided
	if *branch != "" && *branch != "main" && *branch != "nightly" {
		fmt.Fprintf(os.Stderr, "❌ Invalid branch: %s (must be 'main' or 'nightly')\n", *branch)
		os.Exit(1)
	}

	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production upgrade must be run as root (use sudo)\n")
		os.Exit(1)
	}

	oramaHome := "/home/debros"
	oramaDir := oramaHome + "/.orama"
	fmt.Printf("🔄 Upgrading production installation...\n")
	fmt.Printf("  This will preserve existing configurations and data\n")
	fmt.Printf("  Configurations will be updated to latest format\n\n")

	setup := production.NewProductionSetup(oramaHome, os.Stdout, *force, *branch, *noPull, false)

	// Log if --no-pull is enabled
	if *noPull {
		fmt.Printf("  ⚠️  --no-pull flag enabled: Skipping git clone/pull\n")
		fmt.Printf("     Using existing repository at %s/src\n", oramaHome)
	}

	// If branch was explicitly provided, save it for future upgrades
	if *branch != "" {
		if err := production.SaveBranchPreference(oramaDir, *branch); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to save branch preference: %v\n", err)
		} else {
			fmt.Printf("  Using branch: %s (saved for future upgrades)\n", *branch)
		}
	} else {
		// Show which branch is being used (read from saved preference)
		currentBranch := production.ReadBranchPreference(oramaDir)
		fmt.Printf("  Using branch: %s (from saved preference)\n", currentBranch)
	}

	// Phase 1: Check prerequisites
	fmt.Printf("\n📋 Phase 1: Checking prerequisites...\n")
	if err := setup.Phase1CheckPrerequisites(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Prerequisites check failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 2: Provision environment (ensures directories exist)
	fmt.Printf("\n🛠️  Phase 2: Provisioning environment...\n")
	if err := setup.Phase2ProvisionEnvironment(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Environment provisioning failed: %v\n", err)
		os.Exit(1)
	}

	// Stop services before upgrading binaries (if this is an upgrade)
	if setup.IsUpdate() {
		fmt.Printf("\n⏹️  Stopping services before upgrade...\n")
		serviceController := production.NewSystemdController()
		services := []string{
			"debros-gateway.service",
			"debros-node.service",
			"debros-ipfs-cluster.service",
			"debros-ipfs.service",
			// Note: RQLite is managed by node process, not as separate service
			"debros-olric.service",
		}
		for _, svc := range services {
			unitPath := filepath.Join("/etc/systemd/system", svc)
			if _, err := os.Stat(unitPath); err == nil {
				if err := serviceController.StopService(svc); err != nil {
					fmt.Printf("  ⚠️  Warning: Failed to stop %s: %v\n", svc, err)
				} else {
					fmt.Printf("  ✓ Stopped %s\n", svc)
				}
			}
		}
		// Give services time to shut down gracefully
		time.Sleep(2 * time.Second)
	}

	// Check port availability after stopping services
	if err := ensurePortsAvailable("prod upgrade", defaultPorts()); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Phase 2b: Install/update binaries
	fmt.Printf("\nPhase 2b: Installing/updating binaries...\n")
	if err := setup.Phase2bInstallBinaries(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Binary installation failed: %v\n", err)
		os.Exit(1)
	}

	// Detect existing installation
	if setup.IsUpdate() {
		fmt.Printf("  Detected existing installation\n")
	} else {
		fmt.Printf("  ⚠️  No existing installation detected, treating as fresh install\n")
		fmt.Printf("  Use 'orama install' for fresh installation\n")
	}

	// Phase 3: Ensure secrets exist (preserves existing secrets)
	fmt.Printf("\n🔐 Phase 3: Ensuring secrets...\n")
	if err := setup.Phase3GenerateSecrets(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Secret generation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 4: Regenerate configs (updates to latest format)
	// Preserve existing config settings (bootstrap_peers, domain, join_address, etc.)
	enableHTTPS := false
	domain := ""

	// Helper function to extract multiaddr list from config
	extractPeers := func(configPath string) []string {
		var peers []string
		if data, err := os.ReadFile(configPath); err == nil {
			configStr := string(data)
			inPeersList := false
			for _, line := range strings.Split(configStr, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "bootstrap_peers:") || strings.HasPrefix(trimmed, "peers:") {
					inPeersList = true
					continue
				}
				if inPeersList {
					if strings.HasPrefix(trimmed, "-") {
						// Extract multiaddr after the dash
						parts := strings.SplitN(trimmed, "-", 2)
						if len(parts) > 1 {
							peer := strings.TrimSpace(parts[1])
							peer = strings.Trim(peer, "\"'")
							if peer != "" && strings.HasPrefix(peer, "/") {
								peers = append(peers, peer)
							}
						}
					} else if trimmed == "" || !strings.HasPrefix(trimmed, "-") {
						// End of peers list
						break
					}
				}
			}
		}
		return peers
	}

	// Read existing node config to preserve settings
	// Unified config file name (no bootstrap/node distinction)
	nodeConfigPath := filepath.Join(oramaDir, "configs", "node.yaml")

	// Extract peers from existing node config
	peers := extractPeers(nodeConfigPath)

	// Extract VPS IP and join address from advertise addresses
	vpsIP := ""
	joinAddress := ""
	if data, err := os.ReadFile(nodeConfigPath); err == nil {
		configStr := string(data)
		for _, line := range strings.Split(configStr, "\n") {
			trimmed := strings.TrimSpace(line)
			// Try to extract VPS IP from http_adv_address or raft_adv_address
			// Only set if not already found (first valid IP wins)
			if vpsIP == "" && (strings.HasPrefix(trimmed, "http_adv_address:") || strings.HasPrefix(trimmed, "raft_adv_address:")) {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					addr := strings.TrimSpace(parts[1])
					addr = strings.Trim(addr, "\"'")
					if addr != "" && addr != "null" && addr != "localhost:5001" && addr != "localhost:7001" {
						// Extract IP from address (format: "IP:PORT" or "[IPv6]:PORT")
						if host, _, err := net.SplitHostPort(addr); err == nil && host != "" && host != "localhost" {
							vpsIP = host
							// Continue loop to also check for join address
						}
					}
				}
			}
			// Extract join address
			if strings.HasPrefix(trimmed, "rqlite_join_address:") {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					joinAddress = strings.TrimSpace(parts[1])
					joinAddress = strings.Trim(joinAddress, "\"'")
					if joinAddress == "null" || joinAddress == "" {
						joinAddress = ""
					}
				}
			}
		}
	}

	// Read existing gateway config to preserve domain and HTTPS settings
	gatewayConfigPath := filepath.Join(oramaDir, "configs", "gateway.yaml")
	if data, err := os.ReadFile(gatewayConfigPath); err == nil {
		configStr := string(data)
		if strings.Contains(configStr, "domain:") {
			for _, line := range strings.Split(configStr, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "domain:") {
					parts := strings.SplitN(trimmed, ":", 2)
					if len(parts) > 1 {
						domain = strings.TrimSpace(parts[1])
						if domain != "" && domain != "\"\"" && domain != "''" && domain != "null" {
							domain = strings.Trim(domain, "\"'")
							enableHTTPS = true
						} else {
							domain = ""
						}
					}
					break
				}
			}
		}
	}

	fmt.Printf("  Preserving existing configuration:\n")
	if len(peers) > 0 {
		fmt.Printf("    - Peers: %d peer(s) preserved\n", len(peers))
	}
	if vpsIP != "" {
		fmt.Printf("    - VPS IP: %s\n", vpsIP)
	}
	if domain != "" {
		fmt.Printf("    - Domain: %s\n", domain)
	}
	if joinAddress != "" {
		fmt.Printf("    - Join address: %s\n", joinAddress)
	}

	// Phase 2c: Ensure services are properly initialized (fixes existing repos)
	// Now that we have peers and VPS IP, we can properly configure IPFS Cluster
	fmt.Printf("\nPhase 2c: Ensuring services are properly initialized...\n")
	if err := setup.Phase2cInitializeServices(peers, vpsIP); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service initialization failed: %v\n", err)
		os.Exit(1)
	}

	if err := setup.Phase4GenerateConfigs(peers, vpsIP, enableHTTPS, domain, joinAddress); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Config generation warning: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Existing configs preserved\n")
	}

	// Phase 5: Update systemd services
	fmt.Printf("\n🔧 Phase 5: Updating systemd services...\n")
	if err := setup.Phase5CreateSystemdServices(enableHTTPS); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Service update warning: %v\n", err)
	}

	fmt.Printf("\n✅ Upgrade complete!\n")
	if *restartServices {
		fmt.Printf("   Restarting services...\n")
		// Reload systemd daemon
		if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
			fmt.Fprintf(os.Stderr, "   ⚠️  Warning: Failed to reload systemd daemon: %v\n", err)
		}
		// Restart services to apply changes - use getProductionServices to only restart existing services
		services := getProductionServices()
		if len(services) == 0 {
			fmt.Printf("   ⚠️  No services found to restart\n")
		} else {
			for _, svc := range services {
				if err := exec.Command("systemctl", "restart", svc).Run(); err != nil {
					fmt.Printf("   ⚠️  Failed to restart %s: %v\n", svc, err)
				} else {
					fmt.Printf("   ✓ Restarted %s\n", svc)
				}
			}
			fmt.Printf("   ✓ All services restarted\n")
			// Verify services are healthy after restart with exponential backoff
			fmt.Printf("   ⏳ Verifying services are healthy...\n")
			if err := verifyProductionRuntimeWithRetry("prod upgrade --restart", 5, 3*time.Second); err != nil {
				fmt.Fprintf(os.Stderr, "❌ %v\n", err)
				fmt.Fprintf(os.Stderr, "   Upgrade completed but services are not healthy. Check logs with: orama logs <service>\n")
				os.Exit(1)
			}
			fmt.Printf("   ✅ All services verified healthy\n")
		}
	} else {
		fmt.Printf("   To apply changes, restart services:\n")
		fmt.Printf("   sudo systemctl daemon-reload\n")
		fmt.Printf("   sudo systemctl restart debros-*\n")
	}
	fmt.Printf("\n")
}

func handleProdStatus() {
	fmt.Printf("Production Environment Status\n\n")

	// Unified service names (no bootstrap/node distinction)
	serviceNames := []string{
		"debros-ipfs",
		"debros-ipfs-cluster",
		// Note: RQLite is managed by node process, not as separate service
		"debros-olric",
		"debros-node",
		"debros-gateway",
	}

	// Friendly descriptions
	descriptions := map[string]string{
		"debros-ipfs":         "IPFS Daemon",
		"debros-ipfs-cluster": "IPFS Cluster",
		"debros-olric":        "Olric Cache Server",
		"debros-node":         "DeBros Node (includes RQLite)",
		"debros-gateway":      "DeBros Gateway",
	}

	fmt.Printf("Services:\n")
	found := false
	for _, svc := range serviceNames {
		cmd := exec.Command("systemctl", "is-active", "--quiet", svc)
		err := cmd.Run()
		status := "❌ Inactive"
		if err == nil {
			status = "✅ Active"
			found = true
		}
		fmt.Printf("  %s: %s\n", status, descriptions[svc])
	}

	if !found {
		fmt.Printf("  (No services found - installation may be incomplete)\n")
	}

	fmt.Printf("\nDirectories:\n")
	oramaDir := "/home/debros/.orama"
	if _, err := os.Stat(oramaDir); err == nil {
		fmt.Printf("  ✅ %s exists\n", oramaDir)
	} else {
		fmt.Printf("  ❌ %s not found\n", oramaDir)
	}

	fmt.Printf("\nView logs with: dbn prod logs <service>\n")
}

// resolveServiceName resolves service aliases to actual systemd service names
func resolveServiceName(alias string) ([]string, error) {
	// Service alias mapping (unified - no bootstrap/node distinction)
	aliases := map[string][]string{
		"node":         {"debros-node"},
		"ipfs":         {"debros-ipfs"},
		"cluster":      {"debros-ipfs-cluster"},
		"ipfs-cluster": {"debros-ipfs-cluster"},
		"gateway":      {"debros-gateway"},
		"olric":        {"debros-olric"},
		"rqlite":       {"debros-node"}, // RQLite logs are in node logs
	}

	// Check if it's an alias
	if serviceNames, ok := aliases[strings.ToLower(alias)]; ok {
		// Filter to only existing services
		var existing []string
		for _, svc := range serviceNames {
			unitPath := filepath.Join("/etc/systemd/system", svc+".service")
			if _, err := os.Stat(unitPath); err == nil {
				existing = append(existing, svc)
			}
		}
		if len(existing) == 0 {
			return nil, fmt.Errorf("no services found for alias %q", alias)
		}
		return existing, nil
	}

	// Check if it's already a full service name
	unitPath := filepath.Join("/etc/systemd/system", alias+".service")
	if _, err := os.Stat(unitPath); err == nil {
		return []string{alias}, nil
	}

	// Try without .service suffix
	if !strings.HasSuffix(alias, ".service") {
		unitPath = filepath.Join("/etc/systemd/system", alias+".service")
		if _, err := os.Stat(unitPath); err == nil {
			return []string{alias}, nil
		}
	}

	return nil, fmt.Errorf("service %q not found. Use: node, ipfs, cluster, gateway, olric, or full service name", alias)
}

func handleProdLogs(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: dbn prod logs <service> [--follow]\n")
		fmt.Fprintf(os.Stderr, "\nService aliases:\n")
		fmt.Fprintf(os.Stderr, "  node, ipfs, cluster, gateway, olric\n")
		fmt.Fprintf(os.Stderr, "\nOr use full service name:\n")
		fmt.Fprintf(os.Stderr, "  debros-node, debros-gateway, etc.\n")
		os.Exit(1)
	}

	serviceAlias := args[0]
	follow := false
	if len(args) > 1 && (args[1] == "--follow" || args[1] == "-f") {
		follow = true
	}

	// Resolve service alias to actual service names
	serviceNames, err := resolveServiceName(serviceAlias)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		fmt.Fprintf(os.Stderr, "\nAvailable service aliases: node, ipfs, cluster, gateway, olric\n")
		fmt.Fprintf(os.Stderr, "Or use full service name like: debros-node\n")
		os.Exit(1)
	}

	// If multiple services match, show all of them
	if len(serviceNames) > 1 {
		if follow {
			fmt.Fprintf(os.Stderr, "⚠️  Multiple services match alias %q:\n", serviceAlias)
			for _, svc := range serviceNames {
				fmt.Fprintf(os.Stderr, "  - %s\n", svc)
			}
			fmt.Fprintf(os.Stderr, "\nShowing logs for all matching services...\n\n")
			// Use journalctl with multiple units (build args correctly)
			args := []string{}
			for _, svc := range serviceNames {
				args = append(args, "-u", svc)
			}
			args = append(args, "-f")
			cmd := exec.Command("journalctl", args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			cmd.Run()
		} else {
			for i, svc := range serviceNames {
				if i > 0 {
					fmt.Printf("\n" + strings.Repeat("=", 70) + "\n\n")
				}
				fmt.Printf("📋 Logs for %s:\n\n", svc)
				cmd := exec.Command("journalctl", "-u", svc, "-n", "50")
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				cmd.Run()
			}
		}
		return
	}

	// Single service
	service := serviceNames[0]
	if follow {
		fmt.Printf("Following logs for %s (press Ctrl+C to stop)...\n\n", service)
		cmd := exec.Command("journalctl", "-u", service, "-f")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Run()
	} else {
		cmd := exec.Command("journalctl", "-u", service, "-n", "50")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}
}

// errServiceNotFound marks units that systemd does not know about.
var errServiceNotFound = errors.New("service not found")

type portSpec struct {
	Name string
	Port int
}

var servicePorts = map[string][]portSpec{
	"debros-gateway":      {{"Gateway API", 6001}},
	"debros-olric":        {{"Olric HTTP", 3320}, {"Olric Memberlist", 3322}},
	"debros-node":         {{"RQLite HTTP", 5001}, {"RQLite Raft", 7001}},
	"debros-ipfs":         {{"IPFS API", 4501}, {"IPFS Gateway", 8080}, {"IPFS Swarm", 4101}},
	"debros-ipfs-cluster": {{"IPFS Cluster API", 9094}},
}

// defaultPorts is used for fresh installs/upgrades before unit files exist.
func defaultPorts() []portSpec {
	return []portSpec{
		{"IPFS Swarm", 4001},
		{"IPFS API", 4501},
		{"IPFS Gateway", 8080},
		{"Gateway API", 6001},
		{"RQLite HTTP", 5001},
		{"RQLite Raft", 7001},
		{"IPFS Cluster API", 9094},
		{"Olric HTTP", 3320},
		{"Olric Memberlist", 3322},
	}
}

func isServiceActive(service string) (bool, error) {
	cmd := exec.Command("systemctl", "is-active", "--quiet", service)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 3:
				return false, nil
			case 4:
				return false, errServiceNotFound
			}
		}
		return false, err
	}
	return true, nil
}

func isServiceEnabled(service string) (bool, error) {
	cmd := exec.Command("systemctl", "is-enabled", "--quiet", service)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 1:
				return false, nil // Service is disabled
			case 4:
				return false, errServiceNotFound
			}
		}
		return false, err
	}
	return true, nil
}

func collectPortsForServices(services []string, skipActive bool) ([]portSpec, error) {
	seen := make(map[int]portSpec)
	for _, svc := range services {
		if skipActive {
			active, err := isServiceActive(svc)
			if err != nil {
				return nil, fmt.Errorf("unable to check %s: %w", svc, err)
			}
			if active {
				continue
			}
		}
		for _, spec := range servicePorts[svc] {
			if _, ok := seen[spec.Port]; !ok {
				seen[spec.Port] = spec
			}
		}
	}
	ports := make([]portSpec, 0, len(seen))
	for _, spec := range seen {
		ports = append(ports, spec)
	}
	return ports, nil
}

func ensurePortsAvailable(action string, ports []portSpec) error {
	for _, spec := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", spec.Port))
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use") {
				return fmt.Errorf("%s cannot continue: %s (port %d) is already in use", action, spec.Name, spec.Port)
			}
			return fmt.Errorf("%s cannot continue: failed to inspect %s (port %d): %w", action, spec.Name, spec.Port, err)
		}
		_ = ln.Close()
	}
	return nil
}

func checkHTTP(client *http.Client, method, url, label string) error {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return fmt.Errorf("%s check failed: %w", label, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s check failed: %w", label, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned HTTP %d", label, resp.StatusCode)
	}
	return nil
}

func serviceExists(name string) bool {
	unitPath := filepath.Join("/etc/systemd/system", name+".service")
	_, err := os.Stat(unitPath)
	return err == nil
}

// verifyProductionRuntimeWithRetry verifies services with exponential backoff retries
func verifyProductionRuntimeWithRetry(action string, maxAttempts int, initialWait time.Duration) error {
	wait := initialWait
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = verifyProductionRuntime(action)
		if lastErr == nil {
			return nil
		}

		if attempt < maxAttempts {
			fmt.Printf("  ⏳ Services not ready (attempt %d/%d), waiting %v...\n", attempt, maxAttempts, wait)
			time.Sleep(wait)
			// Exponential backoff with cap at 30 seconds
			wait = wait * 2
			if wait > 30*time.Second {
				wait = 30 * time.Second
			}
		}
	}

	return lastErr
}

func verifyProductionRuntime(action string) error {
	services := getProductionServices()
	issues := make([]string, 0)

	for _, svc := range services {
		active, err := isServiceActive(svc)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s status unknown (%v)", svc, err))
			continue
		}
		if !active {
			issues = append(issues, fmt.Sprintf("%s is inactive", svc))
		}
	}

	client := tlsutil.NewHTTPClient(3 * time.Second)

	if err := checkHTTP(client, "GET", "http://127.0.0.1:5001/status", "RQLite status"); err == nil {
	} else if serviceExists("debros-node") {
		issues = append(issues, err.Error())
	}

	if err := checkHTTP(client, "POST", "http://127.0.0.1:4501/api/v0/version", "IPFS API"); err == nil {
	} else if serviceExists("debros-ipfs") {
		issues = append(issues, err.Error())
	}

	if err := checkHTTP(client, "GET", "http://127.0.0.1:9094/health", "IPFS Cluster"); err == nil {
	} else if serviceExists("debros-ipfs-cluster") {
		issues = append(issues, err.Error())
	}

	if err := checkHTTP(client, "GET", "http://127.0.0.1:6001/health", "Gateway health"); err == nil {
	} else if serviceExists("debros-node") {
		// Gateway is now embedded in node, check debros-node instead
		issues = append(issues, err.Error())
	}

	if err := checkHTTP(client, "GET", "http://127.0.0.1:3320/ping", "Olric ping"); err == nil {
	} else if serviceExists("debros-olric") {
		issues = append(issues, err.Error())
	}

	if len(issues) > 0 {
		return fmt.Errorf("%s verification failed:\n  - %s", action, strings.Join(issues, "\n  - "))
	}
	return nil
}

// getProductionServices returns a list of all DeBros production service names that exist
func getProductionServices() []string {
	// Unified service names (no bootstrap/node distinction)
	allServices := []string{
		"debros-gateway",
		"debros-node",
		"debros-rqlite",
		"debros-olric",
		"debros-ipfs-cluster",
		"debros-ipfs",
		"debros-anyone-client",
	}

	// Filter to only existing services by checking if unit file exists
	var existing []string
	for _, svc := range allServices {
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		if _, err := os.Stat(unitPath); err == nil {
			existing = append(existing, svc)
		}
	}

	return existing
}

func isServiceMasked(service string) (bool, error) {
	cmd := exec.Command("systemctl", "is-enabled", service)
	output, err := cmd.CombinedOutput()
	if err != nil {
		outputStr := string(output)
		if strings.Contains(outputStr, "masked") {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func handleProdStart() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production commands must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Starting all DeBros production services...\n")

	services := getProductionServices()
	if len(services) == 0 {
		fmt.Printf("  ⚠️  No DeBros services found\n")
		return
	}

	// Reset failed state for all services before starting
	// This helps with services that were previously in failed state
	resetArgs := []string{"reset-failed"}
	resetArgs = append(resetArgs, services...)
	exec.Command("systemctl", resetArgs...).Run()

	// Check which services are inactive and need to be started
	inactive := make([]string, 0, len(services))
	for _, svc := range services {
		// Check if service is masked and unmask it
		masked, err := isServiceMasked(svc)
		if err == nil && masked {
			fmt.Printf("  ⚠️  %s is masked, unmasking...\n", svc)
			if err := exec.Command("systemctl", "unmask", svc).Run(); err != nil {
				fmt.Printf("  ⚠️  Failed to unmask %s: %v\n", svc, err)
			} else {
				fmt.Printf("  ✓ Unmasked %s\n", svc)
			}
		}

		active, err := isServiceActive(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check %s: %v\n", svc, err)
			continue
		}
		if active {
			fmt.Printf("  ℹ️  %s already running\n", svc)
			// Re-enable if disabled (in case it was stopped with 'dbn prod stop')
			enabled, err := isServiceEnabled(svc)
			if err == nil && !enabled {
				if err := exec.Command("systemctl", "enable", svc).Run(); err != nil {
					fmt.Printf("  ⚠️  Failed to re-enable %s: %v\n", svc, err)
				} else {
					fmt.Printf("  ✓ Re-enabled %s (will auto-start on boot)\n", svc)
				}
			}
			continue
		}
		inactive = append(inactive, svc)
	}

	if len(inactive) == 0 {
		fmt.Printf("\n✅ All services already running\n")
		return
	}

	// Check port availability for services we're about to start
	ports, err := collectPortsForServices(inactive, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
	if err := ensurePortsAvailable("prod start", ports); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Enable and start inactive services
	for _, svc := range inactive {
		// Re-enable the service first (in case it was disabled by 'dbn prod stop')
		enabled, err := isServiceEnabled(svc)
		if err == nil && !enabled {
			if err := exec.Command("systemctl", "enable", svc).Run(); err != nil {
				fmt.Printf("  ⚠️  Failed to enable %s: %v\n", svc, err)
			} else {
				fmt.Printf("  ✓ Enabled %s (will auto-start on boot)\n", svc)
			}
		}

		// Start the service
		if err := exec.Command("systemctl", "start", svc).Run(); err != nil {
			fmt.Printf("  ⚠️  Failed to start %s: %v\n", svc, err)
		} else {
			fmt.Printf("  ✓ Started %s\n", svc)
		}
	}

	// Give services more time to fully initialize before verification
	// Some services may need more time to start up, especially if they're
	// waiting for dependencies or initializing databases
	fmt.Printf("  ⏳ Waiting for services to initialize...\n")
	time.Sleep(5 * time.Second)

	// Verify all services are healthy with exponential backoff retries
	fmt.Printf("  ⏳ Verifying services are healthy...\n")
	if err := verifyProductionRuntimeWithRetry("prod start", 6, 2*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		fmt.Fprintf(os.Stderr, "\n   Services may still be starting. Check status with:\n")
		fmt.Fprintf(os.Stderr, "   systemctl status debros-*\n")
		fmt.Fprintf(os.Stderr, "   orama logs <service>\n")
		os.Exit(1)
	}

	fmt.Printf("\n✅ All services started and healthy\n")
}

func handleProdStop() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production commands must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Stopping all DeBros production services...\n")

	services := getProductionServices()
	if len(services) == 0 {
		fmt.Printf("  ⚠️  No DeBros services found\n")
		return
	}

	// First, disable all services to prevent auto-restart
	disableArgs := []string{"disable"}
	disableArgs = append(disableArgs, services...)
	if err := exec.Command("systemctl", disableArgs...).Run(); err != nil {
		fmt.Printf("  ⚠️  Warning: Failed to disable some services: %v\n", err)
	}

	// Stop all services at once using a single systemctl command
	// This is more efficient and ensures they all stop together
	stopArgs := []string{"stop"}
	stopArgs = append(stopArgs, services...)
	if err := exec.Command("systemctl", stopArgs...).Run(); err != nil {
		fmt.Printf("  ⚠️  Warning: Some services may have failed to stop: %v\n", err)
		// Continue anyway - we'll verify and handle individually below
	}

	// Wait a moment for services to fully stop
	time.Sleep(2 * time.Second)

	// Reset failed state for any services that might be in failed state
	resetArgs := []string{"reset-failed"}
	resetArgs = append(resetArgs, services...)
	exec.Command("systemctl", resetArgs...).Run()

	// Wait again after reset-failed
	time.Sleep(1 * time.Second)

	// Stop again to ensure they're stopped
	exec.Command("systemctl", stopArgs...).Run()
	time.Sleep(1 * time.Second)

	hadError := false
	for _, svc := range services {
		active, err := isServiceActive(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check %s: %v\n", svc, err)
			hadError = true
			continue
		}
		if !active {
			fmt.Printf("  ✓ Stopped %s\n", svc)
		} else {
			// Service is still active, try stopping it individually
			fmt.Printf("  ⚠️  %s still active, attempting individual stop...\n", svc)
			if err := exec.Command("systemctl", "stop", svc).Run(); err != nil {
				fmt.Printf("  ❌  Failed to stop %s: %v\n", svc, err)
				hadError = true
			} else {
				// Wait and verify again
				time.Sleep(1 * time.Second)
				if stillActive, _ := isServiceActive(svc); stillActive {
					fmt.Printf("  ❌  %s restarted itself (Restart=always)\n", svc)
					hadError = true
				} else {
					fmt.Printf("  ✓ Stopped %s\n", svc)
				}
			}
		}

		// Disable the service to prevent it from auto-starting on boot
		enabled, err := isServiceEnabled(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check if %s is enabled: %v\n", svc, err)
			// Continue anyway - try to disable
		}
		if enabled {
			if err := exec.Command("systemctl", "disable", svc).Run(); err != nil {
				fmt.Printf("  ⚠️  Failed to disable %s: %v\n", svc, err)
				hadError = true
			} else {
				fmt.Printf("  ✓ Disabled %s (will not auto-start on boot)\n", svc)
			}
		} else {
			fmt.Printf("  ℹ️  %s already disabled\n", svc)
		}
	}

	if hadError {
		fmt.Fprintf(os.Stderr, "\n⚠️  Some services may still be restarting due to Restart=always\n")
		fmt.Fprintf(os.Stderr, "   Check status with: systemctl list-units 'debros-*'\n")
		fmt.Fprintf(os.Stderr, "   If services are still restarting, they may need manual intervention\n")
	} else {
		fmt.Printf("\n✅ All services stopped and disabled (will not auto-start on boot)\n")
		fmt.Printf("   Use 'dbn prod start' to start and re-enable services\n")
	}
}

func handleProdRestart() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production commands must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Restarting all DeBros production services...\n")

	services := getProductionServices()
	if len(services) == 0 {
		fmt.Printf("  ⚠️  No DeBros services found\n")
		return
	}

	// Stop all active services first
	fmt.Printf("  Stopping services...\n")
	for _, svc := range services {
		active, err := isServiceActive(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check %s: %v\n", svc, err)
			continue
		}
		if !active {
			fmt.Printf("  ℹ️  %s was already stopped\n", svc)
			continue
		}
		if err := exec.Command("systemctl", "stop", svc).Run(); err != nil {
			fmt.Printf("  ⚠️  Failed to stop %s: %v\n", svc, err)
		} else {
			fmt.Printf("  ✓ Stopped %s\n", svc)
		}
	}

	// Check port availability before restarting
	ports, err := collectPortsForServices(services, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
	if err := ensurePortsAvailable("prod restart", ports); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Start all services
	fmt.Printf("  Starting services...\n")
	for _, svc := range services {
		if err := exec.Command("systemctl", "start", svc).Run(); err != nil {
			fmt.Printf("  ⚠️  Failed to start %s: %v\n", svc, err)
		} else {
			fmt.Printf("  ✓ Started %s\n", svc)
		}
	}

	// Verify all services are healthy with exponential backoff retries
	fmt.Printf("  ⏳ Verifying services are healthy...\n")
	if err := verifyProductionRuntimeWithRetry("prod restart", 5, 3*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ All services restarted and healthy\n")
}

func handleProdUninstall() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production uninstall must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("⚠️  This will stop and remove all DeBros production services\n")
	fmt.Printf("⚠️  Configuration and data will be preserved in /home/debros/.orama\n\n")
	fmt.Printf("Continue? (yes/no): ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	if response != "yes" && response != "y" {
		fmt.Printf("Uninstall cancelled\n")
		return
	}

	services := []string{
		"debros-gateway",
		"debros-node",
		"debros-rqlite",
		"debros-olric",
		"debros-ipfs-cluster",
		"debros-ipfs",
		"debros-anyone-client",
	}

	fmt.Printf("Stopping services...\n")
	for _, svc := range services {
		exec.Command("systemctl", "stop", svc).Run()
		exec.Command("systemctl", "disable", svc).Run()
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		os.Remove(unitPath)
	}

	exec.Command("systemctl", "daemon-reload").Run()
	fmt.Printf("✅ Services uninstalled\n")
	fmt.Printf("   Configuration and data preserved in /home/debros/.orama\n")
	fmt.Printf("   To remove all data: rm -rf /home/debros/.orama\n\n")
}

// handleProdMigrate migrates from old unified setup to new unified setup
func handleProdMigrate(args []string) {
	// Parse flags
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "Show what would be migrated without making changes")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "❌ Failed to parse flags: %v\n", err)
		os.Exit(1)
	}

	if os.Geteuid() != 0 && !*dryRun {
		fmt.Fprintf(os.Stderr, "❌ Migration must be run as root (use sudo)\n")
		os.Exit(1)
	}

	oramaDir := "/home/debros/.orama"

	fmt.Printf("🔄 Checking for installations to migrate...\n\n")

	// Check for old-style installations
	oldDataDirs := []string{
		filepath.Join(oramaDir, "data", "node-1"),
		filepath.Join(oramaDir, "data", "node"),
	}

	oldServices := []string{
		"debros-ipfs",
		"debros-ipfs-cluster",
		"debros-node",
	}

	oldConfigs := []string{
		filepath.Join(oramaDir, "configs", "bootstrap.yaml"),
	}

	// Check what needs to be migrated
	var needsMigration bool

	fmt.Printf("Checking data directories:\n")
	for _, dir := range oldDataDirs {
		if _, err := os.Stat(dir); err == nil {
			fmt.Printf("  ⚠️  Found old directory: %s\n", dir)
			needsMigration = true
		}
	}

	fmt.Printf("\nChecking services:\n")
	for _, svc := range oldServices {
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		if _, err := os.Stat(unitPath); err == nil {
			fmt.Printf("  ⚠️  Found old service: %s\n", svc)
			needsMigration = true
		}
	}

	fmt.Printf("\nChecking configs:\n")
	for _, cfg := range oldConfigs {
		if _, err := os.Stat(cfg); err == nil {
			fmt.Printf("  ⚠️  Found old config: %s\n", cfg)
			needsMigration = true
		}
	}

	if !needsMigration {
		fmt.Printf("\n✅ No migration needed - installation already uses unified structure\n")
		return
	}

	if *dryRun {
		fmt.Printf("\n📋 Dry run - no changes made\n")
		fmt.Printf("   Run without --dry-run to perform migration\n")
		return
	}

	fmt.Printf("\n🔄 Starting migration...\n")

	// Stop old services first
	fmt.Printf("\n  Stopping old services...\n")
	for _, svc := range oldServices {
		if err := exec.Command("systemctl", "stop", svc).Run(); err == nil {
			fmt.Printf("    ✓ Stopped %s\n", svc)
		}
	}

	// Migrate data directories
	newDataDir := filepath.Join(oramaDir, "data")
	fmt.Printf("\n  Migrating data directories...\n")

	// Prefer node-1 data if it exists, otherwise use node data
	sourceDir := ""
	if _, err := os.Stat(filepath.Join(oramaDir, "data", "node-1")); err == nil {
		sourceDir = filepath.Join(oramaDir, "data", "node-1")
	} else if _, err := os.Stat(filepath.Join(oramaDir, "data", "node")); err == nil {
		sourceDir = filepath.Join(oramaDir, "data", "node")
	}

	if sourceDir != "" {
		// Move contents to unified data directory
		entries, _ := os.ReadDir(sourceDir)
		for _, entry := range entries {
			src := filepath.Join(sourceDir, entry.Name())
			dst := filepath.Join(newDataDir, entry.Name())
			if _, err := os.Stat(dst); os.IsNotExist(err) {
				if err := os.Rename(src, dst); err == nil {
					fmt.Printf("    ✓ Moved %s → %s\n", src, dst)
				}
			}
		}
	}

	// Remove old data directories
	for _, dir := range oldDataDirs {
		if err := os.RemoveAll(dir); err == nil {
			fmt.Printf("    ✓ Removed %s\n", dir)
		}
	}

	// Migrate config files
	fmt.Printf("\n  Migrating config files...\n")
	oldNodeConfig := filepath.Join(oramaDir, "configs", "bootstrap.yaml")
	newNodeConfig := filepath.Join(oramaDir, "configs", "node.yaml")
	if _, err := os.Stat(oldNodeConfig); err == nil {
		if _, err := os.Stat(newNodeConfig); os.IsNotExist(err) {
			if err := os.Rename(oldNodeConfig, newNodeConfig); err == nil {
				fmt.Printf("    ✓ Renamed bootstrap.yaml → node.yaml\n")
			}
		} else {
			os.Remove(oldNodeConfig)
			fmt.Printf("    ✓ Removed old bootstrap.yaml (node.yaml already exists)\n")
		}
	}

	// Remove old services
	fmt.Printf("\n  Removing old service files...\n")
	for _, svc := range oldServices {
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		if err := os.Remove(unitPath); err == nil {
			fmt.Printf("    ✓ Removed %s\n", unitPath)
		}
	}

	// Reload systemd
	exec.Command("systemctl", "daemon-reload").Run()

	fmt.Printf("\n✅ Migration complete!\n")
	fmt.Printf("   Run 'sudo orama upgrade --restart' to regenerate services with new names\n\n")
}
