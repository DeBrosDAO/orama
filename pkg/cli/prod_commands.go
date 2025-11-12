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

	"github.com/DeBrosOfficial/network/pkg/environments/production"
	"github.com/multiformats/go-multiaddr"
)

// normalizeBootstrapPeers normalizes and validates bootstrap peer multiaddrs
func normalizeBootstrapPeers(peersStr string) ([]string, error) {
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
	fmt.Printf("Usage: dbn prod <subcommand> [options]\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  install                   - Full production bootstrap (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --force               - Reconfigure all settings\n")
	fmt.Printf("      --bootstrap           - Install as bootstrap node\n")
	fmt.Printf("      --vps-ip IP           - VPS public IP address (required for non-bootstrap)\n")
	fmt.Printf("      --peers ADDRS         - Comma-separated bootstrap peer multiaddrs (required for non-bootstrap)\n")
	fmt.Printf("      --bootstrap-join ADDR - Bootstrap raft join address (for secondary bootstrap)\n")
	fmt.Printf("      --domain DOMAIN       - Domain for HTTPS (optional)\n")
	fmt.Printf("      --branch BRANCH       - Git branch to use (main or nightly, default: main)\n")
	fmt.Printf("      --ignore-resource-checks - Skip disk/RAM/CPU prerequisite validation\n")
	fmt.Printf("  upgrade                   - Upgrade existing installation (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --restart              - Automatically restart services after upgrade\n")
	fmt.Printf("      --branch BRANCH        - Git branch to use (main or nightly, uses saved preference if not specified)\n")
	fmt.Printf("      --no-pull              - Skip git clone/pull, use existing /home/debros/src\n")
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
	fmt.Printf("  # Bootstrap node (main branch)\n")
	fmt.Printf("  sudo dbn prod install --bootstrap\n\n")
	fmt.Printf("  # Bootstrap node (nightly branch)\n")
	fmt.Printf("  sudo dbn prod install --bootstrap --branch nightly\n\n")
	fmt.Printf("  # Join existing cluster\n")
	fmt.Printf("  sudo dbn prod install --vps-ip 10.0.0.2 --peers /ip4/10.0.0.1/tcp/4001/p2p/Qm...\n\n")
	fmt.Printf("  # Secondary bootstrap joining existing cluster\n")
	fmt.Printf("  sudo dbn prod install --bootstrap --vps-ip 10.0.0.2 --bootstrap-join 10.0.0.1:7001 --peers /ip4/10.0.0.1/tcp/4001/p2p/Qm...\n\n")
	fmt.Printf("  # Upgrade using saved branch preference\n")
	fmt.Printf("  sudo dbn prod upgrade --restart\n\n")
	fmt.Printf("  # Upgrade and switch to nightly branch\n")
	fmt.Printf("  sudo dbn prod upgrade --restart --branch nightly\n\n")
	fmt.Printf("  # Upgrade without pulling latest code (use existing /home/debros/src)\n")
	fmt.Printf("  sudo dbn prod upgrade --restart --no-pull\n\n")
	fmt.Printf("  # Service management\n")
	fmt.Printf("  sudo dbn prod start\n")
	fmt.Printf("  sudo dbn prod stop\n")
	fmt.Printf("  sudo dbn prod restart\n\n")
	fmt.Printf("  dbn prod status\n")
	fmt.Printf("  dbn prod logs node --follow\n")
	fmt.Printf("  dbn prod logs gateway --follow\n")
}

func handleProdInstall(args []string) {
	// Parse arguments using flag.FlagSet
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	force := fs.Bool("force", false, "Reconfigure all settings")
	isBootstrap := fs.Bool("bootstrap", false, "Install as bootstrap node")
	skipResourceChecks := fs.Bool("ignore-resource-checks", false, "Skip disk/RAM/CPU prerequisite validation")
	vpsIP := fs.String("vps-ip", "", "VPS public IP address (required for non-bootstrap)")
	domain := fs.String("domain", "", "Domain for HTTPS (optional)")
	peersStr := fs.String("peers", "", "Comma-separated bootstrap peer multiaddrs (required for non-bootstrap)")
	bootstrapJoin := fs.String("bootstrap-join", "", "Bootstrap raft join address (for secondary bootstrap)")
	branch := fs.String("branch", "main", "Git branch to use (main or nightly)")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "❌ Failed to parse flags: %v\n", err)
		os.Exit(1)
	}

	// Validate branch
	if *branch != "main" && *branch != "nightly" {
		fmt.Fprintf(os.Stderr, "❌ Invalid branch: %s (must be 'main' or 'nightly')\n", *branch)
		os.Exit(1)
	}

	// Normalize and validate bootstrap peers
	bootstrapPeers, err := normalizeBootstrapPeers(*peersStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Invalid bootstrap peers: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Example: --peers /ip4/10.0.0.1/tcp/4001/p2p/Qm...,/ip4/10.0.0.2/tcp/4001/p2p/Qm...\n")
		os.Exit(1)
	}

	// Validate setup requirements
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production install must be run as root (use sudo)\n")
		os.Exit(1)
	}

	// Validate bootstrap node requirements
	if *isBootstrap {
		if *vpsIP == "" {
			fmt.Fprintf(os.Stderr, "❌ --vps-ip is required for bootstrap nodes\n")
			fmt.Fprintf(os.Stderr, "   Bootstrap nodes must advertise a public IP address for other nodes to connect\n")
			fmt.Fprintf(os.Stderr, "   Usage: sudo dbn prod install --bootstrap --vps-ip <public_ip>\n")
			fmt.Fprintf(os.Stderr, "   Example: sudo dbn prod install --bootstrap --vps-ip 203.0.113.1\n")
			os.Exit(1)
		}
		// Validate secondary bootstrap requirements
		if *bootstrapJoin == "" {
			fmt.Fprintf(os.Stderr, "⚠️  Warning: Primary bootstrap node detected (--bootstrap without --bootstrap-join)\n")
			fmt.Fprintf(os.Stderr, "   This node will form a new cluster. To join existing cluster as secondary bootstrap:\n")
			fmt.Fprintf(os.Stderr, "   sudo dbn prod install --bootstrap --vps-ip %s --bootstrap-join <bootstrap_ip>:7001 --peers <multiaddr>\n", *vpsIP)
		}
	}

	// Validate non-bootstrap node requirements
	if !*isBootstrap {
		if *vpsIP == "" {
			fmt.Fprintf(os.Stderr, "❌ --vps-ip is required for non-bootstrap nodes\n")
			fmt.Fprintf(os.Stderr, "   Usage: sudo dbn prod install --vps-ip <public_ip> --peers <multiaddr>\n")
			os.Exit(1)
		}
		if len(bootstrapPeers) == 0 {
			fmt.Fprintf(os.Stderr, "❌ --peers is required for non-bootstrap nodes\n")
			fmt.Fprintf(os.Stderr, "   Usage: sudo dbn prod install --vps-ip <public_ip> --peers <multiaddr>\n")
			fmt.Fprintf(os.Stderr, "   Example: --peers /ip4/10.0.0.1/tcp/4001/p2p/Qm...\n")
			os.Exit(1)
		}
	}

	debrosHome := "/home/debros"
	debrosDir := debrosHome + "/.debros"
	setup := production.NewProductionSetup(debrosHome, os.Stdout, *force, *branch, false, *skipResourceChecks)

	// Check port availability before proceeding
	if err := ensurePortsAvailable("prod install", defaultPorts()); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Save branch preference for future upgrades
	if err := production.SaveBranchPreference(debrosDir, *branch); err != nil {
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

	// Determine node type early
	nodeType := "node"
	if *isBootstrap {
		nodeType = "bootstrap"
	}

	// Phase 3: Generate secrets FIRST (before service initialization)
	// This ensures cluster secret and swarm key exist before repos are seeded
	fmt.Printf("\n🔐 Phase 3: Generating secrets...\n")
	if err := setup.Phase3GenerateSecrets(*isBootstrap); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Secret generation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 2c: Initialize services (after secrets are in place)
	fmt.Printf("\nPhase 2c: Initializing services...\n")
	if err := setup.Phase2cInitializeServices(nodeType); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service initialization failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 4: Generate configs
	fmt.Printf("\n⚙️  Phase 4: Generating configurations...\n")
	enableHTTPS := *domain != ""
	if err := setup.Phase4GenerateConfigs(*isBootstrap, bootstrapPeers, *vpsIP, enableHTTPS, *domain, *bootstrapJoin); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Configuration generation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 5: Create systemd services
	fmt.Printf("\n🔧 Phase 5: Creating systemd services...\n")
	if err := setup.Phase5CreateSystemdServices(nodeType, *vpsIP); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service creation failed: %v\n", err)
		os.Exit(1)
	}

	// Give services a moment to fully initialize before verification
	fmt.Printf("\n⏳ Waiting for services to initialize...\n")
	time.Sleep(5 * time.Second)

	// Verify all services are running correctly
	if err := verifyProductionRuntime("prod install"); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		fmt.Fprintf(os.Stderr, "   Installation completed but services are not healthy. Check logs with: dbn prod logs <service>\n")
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

	debrosHome := "/home/debros"
	debrosDir := debrosHome + "/.debros"
	fmt.Printf("🔄 Upgrading production installation...\n")
	fmt.Printf("  This will preserve existing configurations and data\n")
	fmt.Printf("  Configurations will be updated to latest format\n\n")

	setup := production.NewProductionSetup(debrosHome, os.Stdout, *force, *branch, *noPull, false)

	// Log if --no-pull is enabled
	if *noPull {
		fmt.Printf("  ⚠️  --no-pull flag enabled: Skipping git clone/pull\n")
		fmt.Printf("     Using existing repository at %s/src\n", debrosHome)
	}

	// If branch was explicitly provided, save it for future upgrades
	if *branch != "" {
		if err := production.SaveBranchPreference(debrosDir, *branch); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to save branch preference: %v\n", err)
		} else {
			fmt.Printf("  Using branch: %s (saved for future upgrades)\n", *branch)
		}
	} else {
		// Show which branch is being used (read from saved preference)
		currentBranch := production.ReadBranchPreference(debrosDir)
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
			"debros-node-bootstrap.service",
			"debros-node-node.service",
			"debros-ipfs-cluster-bootstrap.service",
			"debros-ipfs-cluster-node.service",
			"debros-ipfs-bootstrap.service",
			"debros-ipfs-node.service",
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

	// Detect node type from existing installation
	nodeType := "node"
	if setup.IsUpdate() {
		// Check if bootstrap config exists
		bootstrapConfig := filepath.Join("/home/debros/.debros", "configs", "bootstrap.yaml")
		if _, err := os.Stat(bootstrapConfig); err == nil {
			nodeType = "bootstrap"
		} else {
			// Check data directory structure
			bootstrapDataPath := filepath.Join("/home/debros/.debros", "data", "bootstrap")
			if _, err := os.Stat(bootstrapDataPath); err == nil {
				nodeType = "bootstrap"
			}
		}
		fmt.Printf("  Detected node type: %s\n", nodeType)
	} else {
		fmt.Printf("  ⚠️  No existing installation detected, treating as fresh install\n")
		fmt.Printf("  Use 'dbn prod install --bootstrap' for fresh bootstrap installation\n")
		nodeType = "bootstrap" // Default for upgrade if nothing exists
	}

	// Phase 2c: Ensure services are properly initialized (fixes existing repos)
	fmt.Printf("\nPhase 2c: Ensuring services are properly initialized...\n")
	if err := setup.Phase2cInitializeServices(nodeType); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service initialization failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 3: Ensure secrets exist (preserves existing secrets)
	fmt.Printf("\n🔐 Phase 3: Ensuring secrets...\n")
	if err := setup.Phase3GenerateSecrets(nodeType == "bootstrap"); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Secret generation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 4: Regenerate configs (updates to latest format)
	// Preserve existing config settings (bootstrap_peers, domain, join_address, etc.)
	enableHTTPS := false
	domain := ""
	bootstrapJoin := ""

	// Helper function to extract multiaddr list from config
	extractBootstrapPeers := func(configPath string) []string {
		var peers []string
		if data, err := os.ReadFile(configPath); err == nil {
			configStr := string(data)
			inBootstrapPeers := false
			for _, line := range strings.Split(configStr, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "bootstrap_peers:") || strings.HasPrefix(trimmed, "bootstrap peers:") {
					inBootstrapPeers = true
					continue
				}
				if inBootstrapPeers {
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
						// End of bootstrap_peers list
						break
					}
				}
			}
		}
		return peers
	}

	// Read existing node config to preserve bootstrap_peers and join_address
	nodeConfigFile := "bootstrap.yaml"
	if nodeType == "node" {
		nodeConfigFile = "node.yaml"
	}
	nodeConfigPath := filepath.Join(debrosDir, "configs", nodeConfigFile)

	// Extract bootstrap peers from existing node config
	bootstrapPeers := extractBootstrapPeers(nodeConfigPath)

	// Extract VPS IP from advertise addresses and bootstrap join address
	vpsIP := ""
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
							// Continue loop to also check for bootstrap join address
						}
					}
				}
			}
			// Extract bootstrap join address if it's a bootstrap node
			if nodeType == "bootstrap" && strings.HasPrefix(trimmed, "rqlite_join_address:") {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					bootstrapJoin = strings.TrimSpace(parts[1])
					bootstrapJoin = strings.Trim(bootstrapJoin, "\"'")
					if bootstrapJoin == "null" || bootstrapJoin == "" {
						bootstrapJoin = ""
					}
				}
			}
		}
	}

	// Read existing gateway config to preserve domain and HTTPS settings
	gatewayConfigPath := filepath.Join(debrosDir, "configs", "gateway.yaml")
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
	if len(bootstrapPeers) > 0 {
		fmt.Printf("    - Bootstrap peers: %d peer(s) preserved\n", len(bootstrapPeers))
	}
	if vpsIP != "" {
		fmt.Printf("    - VPS IP: %s\n", vpsIP)
	}
	if domain != "" {
		fmt.Printf("    - Domain: %s\n", domain)
	}
	if bootstrapJoin != "" {
		fmt.Printf("    - Bootstrap join address: %s\n", bootstrapJoin)
	}

	if err := setup.Phase4GenerateConfigs(nodeType == "bootstrap", bootstrapPeers, vpsIP, enableHTTPS, domain, bootstrapJoin); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Config generation warning: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Existing configs preserved\n")
	}

	// Phase 5: Update systemd services
	fmt.Printf("\n🔧 Phase 5: Updating systemd services...\n")
	if err := setup.Phase5CreateSystemdServices(nodeType, ""); err != nil {
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
			// Give services a moment to fully initialize before verification
			fmt.Printf("   ⏳ Waiting for services to initialize...\n")
			time.Sleep(5 * time.Second)
			// Verify services are healthy after restart
			if err := verifyProductionRuntime("prod upgrade --restart"); err != nil {
				fmt.Fprintf(os.Stderr, "❌ %v\n", err)
				fmt.Fprintf(os.Stderr, "   Upgrade completed but services are not healthy. Check logs with: dbn prod logs <service>\n")
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

	// Check for all possible service names (bootstrap and node variants)
	serviceNames := []string{
		"debros-ipfs-bootstrap",
		"debros-ipfs-node",
		"debros-ipfs-cluster-bootstrap",
		"debros-ipfs-cluster-node",
		// Note: RQLite is managed by node process, not as separate service
		"debros-olric",
		"debros-node-bootstrap",
		"debros-node-node",
		"debros-gateway",
	}

	// Friendly descriptions
	descriptions := map[string]string{
		"debros-ipfs-bootstrap":         "IPFS Daemon (Bootstrap)",
		"debros-ipfs-node":              "IPFS Daemon (Node)",
		"debros-ipfs-cluster-bootstrap": "IPFS Cluster (Bootstrap)",
		"debros-ipfs-cluster-node":      "IPFS Cluster (Node)",
		"debros-olric":                  "Olric Cache Server",
		"debros-node-bootstrap":         "DeBros Node (Bootstrap) - includes RQLite",
		"debros-node-node":              "DeBros Node (Node) - includes RQLite",
		"debros-gateway":                "DeBros Gateway",
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
	debrosDir := "/home/debros/.debros"
	if _, err := os.Stat(debrosDir); err == nil {
		fmt.Printf("  ✅ %s exists\n", debrosDir)
	} else {
		fmt.Printf("  ❌ %s not found\n", debrosDir)
	}

	fmt.Printf("\nView logs with: dbn prod logs <service>\n")
}

// resolveServiceName resolves service aliases to actual systemd service names
func resolveServiceName(alias string) ([]string, error) {
	// Service alias mapping
	aliases := map[string][]string{
		"node":         {"debros-node-bootstrap", "debros-node-node"},
		"ipfs":         {"debros-ipfs-bootstrap", "debros-ipfs-node"},
		"cluster":      {"debros-ipfs-cluster-bootstrap", "debros-ipfs-cluster-node"},
		"ipfs-cluster": {"debros-ipfs-cluster-bootstrap", "debros-ipfs-cluster-node"},
		"gateway":      {"debros-gateway"},
		"olric":        {"debros-olric"},
		"rqlite":       {"debros-node-bootstrap", "debros-node-node"}, // RQLite logs are in node logs
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
		fmt.Fprintf(os.Stderr, "  debros-node-bootstrap, debros-gateway, etc.\n")
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
		fmt.Fprintf(os.Stderr, "Or use full service name like: debros-node-bootstrap\n")
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
	"debros-gateway":                {{"Gateway API", 6001}},
	"debros-olric":                  {{"Olric HTTP", 3320}, {"Olric Memberlist", 3322}},
	"debros-node-bootstrap":         {{"RQLite HTTP", 5001}, {"RQLite Raft", 7001}, {"IPFS Cluster API", 9094}},
	"debros-node-node":              {{"RQLite HTTP", 5001}, {"RQLite Raft", 7001}, {"IPFS Cluster API", 9094}},
	"debros-ipfs-bootstrap":         {{"IPFS API", 4501}, {"IPFS Gateway", 8080}, {"IPFS Swarm", 4001}},
	"debros-ipfs-node":              {{"IPFS API", 4501}, {"IPFS Gateway", 8080}, {"IPFS Swarm", 4001}},
	"debros-ipfs-cluster-bootstrap": {{"IPFS Cluster API", 9094}},
	"debros-ipfs-cluster-node":      {{"IPFS Cluster API", 9094}},
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

	client := &http.Client{Timeout: 3 * time.Second}

	if err := checkHTTP(client, "GET", "http://127.0.0.1:5001/status", "RQLite status"); err == nil {
	} else if serviceExists("debros-node-bootstrap") || serviceExists("debros-node-node") {
		issues = append(issues, err.Error())
	}

	if err := checkHTTP(client, "POST", "http://127.0.0.1:4501/api/v0/version", "IPFS API"); err == nil {
	} else if serviceExists("debros-ipfs-bootstrap") || serviceExists("debros-ipfs-node") {
		issues = append(issues, err.Error())
	}

	if err := checkHTTP(client, "GET", "http://127.0.0.1:9094/health", "IPFS Cluster"); err == nil {
	} else if serviceExists("debros-ipfs-cluster-bootstrap") || serviceExists("debros-ipfs-cluster-node") {
		issues = append(issues, err.Error())
	}

	if err := checkHTTP(client, "GET", "http://127.0.0.1:6001/health", "Gateway health"); err == nil {
	} else if serviceExists("debros-gateway") {
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
	// All possible service names (both bootstrap and node variants)
	allServices := []string{
		"debros-gateway",
		"debros-node-node",
		"debros-node-bootstrap",
		"debros-olric",
		// Note: RQLite is managed by node process, not as separate service
		"debros-ipfs-cluster-bootstrap",
		"debros-ipfs-cluster-node",
		"debros-ipfs-bootstrap",
		"debros-ipfs-node",
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

	// Check which services are inactive and need to be started
	inactive := make([]string, 0, len(services))
	for _, svc := range services {
		active, err := isServiceActive(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check %s: %v\n", svc, err)
			continue
		}
		if active {
			fmt.Printf("  ℹ️  %s already running\n", svc)
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

	// Start inactive services
	for _, svc := range inactive {
		if err := exec.Command("systemctl", "start", svc).Run(); err != nil {
			fmt.Printf("  ⚠️  Failed to start %s: %v\n", svc, err)
		} else {
			fmt.Printf("  ✓ Started %s\n", svc)
		}
	}

	// Give services a moment to fully initialize before verification
	fmt.Printf("  ⏳ Waiting for services to initialize...\n")
	time.Sleep(3 * time.Second)

	// Verify all services are healthy
	if err := verifyProductionRuntime("prod start"); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
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

	hadError := false
	for _, svc := range services {
		active, err := isServiceActive(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check %s: %v\n", svc, err)
			hadError = true
			continue
		}
		if !active {
			fmt.Printf("  ℹ️  %s already stopped\n", svc)
			continue
		}
		if err := exec.Command("systemctl", "stop", svc).Run(); err != nil {
			fmt.Printf("  ⚠️  Failed to stop %s: %v\n", svc, err)
			hadError = true
			continue
		}
		// Verify the service actually stopped and didn't restart itself
		if stillActive, err := isServiceActive(svc); err != nil {
			fmt.Printf("  ⚠️  Unable to verify %s stop: %v\n", svc, err)
			hadError = true
		} else if stillActive {
			fmt.Printf("  ❌  %s restarted itself immediately\n", svc)
			hadError = true
		} else {
			fmt.Printf("  ✓ Stopped %s\n", svc)
		}
	}

	if hadError {
		fmt.Fprintf(os.Stderr, "\n❌ One or more services failed to stop cleanly\n")
		os.Exit(1)
	}

	fmt.Printf("\n✅ All services stopped and remain inactive\n")
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

	// Give services a moment to fully initialize before verification
	fmt.Printf("  ⏳ Waiting for services to initialize...\n")
	time.Sleep(3 * time.Second)

	// Verify all services are healthy
	if err := verifyProductionRuntime("prod restart"); err != nil {
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
	fmt.Printf("⚠️  Configuration and data will be preserved in /home/debros/.debros\n\n")
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
		"debros-node-node",
		"debros-node-bootstrap",
		"debros-olric",
		// Note: RQLite is managed by node process, not as separate service
		"debros-ipfs-cluster-bootstrap",
		"debros-ipfs-cluster-node",
		"debros-ipfs-bootstrap",
		"debros-ipfs-node",
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
	fmt.Printf("   Configuration and data preserved in /home/debros/.debros\n")
	fmt.Printf("   To remove all data: rm -rf /home/debros/.debros\n\n")
}
