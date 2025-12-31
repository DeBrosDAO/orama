package cli

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
	"github.com/DeBrosOfficial/network/pkg/environments/production"
)

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
	fmt.Printf("      --swarm-key HEX       - 64-hex IPFS swarm key (required when joining)\n")
	fmt.Printf("      --ipfs-peer ID        - IPFS peer ID to connect to (auto-discovered)\n")
	fmt.Printf("      --ipfs-addrs ADDRS    - IPFS swarm addresses (auto-discovered)\n")
	fmt.Printf("      --ipfs-cluster-peer ID - IPFS Cluster peer ID (auto-discovered)\n")
	fmt.Printf("      --ipfs-cluster-addrs ADDRS - IPFS Cluster addresses (auto-discovered)\n")
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
	fmt.Printf("    --cluster-secret <64-hex-secret> --swarm-key <64-hex-swarm-key>\n\n")
	fmt.Printf("  # Upgrade\n")
	fmt.Printf("  sudo orama upgrade --restart\n\n")
	fmt.Printf("  # Service management\n")
	fmt.Printf("  sudo orama start\n")
	fmt.Printf("  sudo orama stop\n")
	fmt.Printf("  sudo orama restart\n\n")
	fmt.Printf("  orama status\n")
	fmt.Printf("  orama logs node --follow\n")
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
	if err := utils.EnsurePortsAvailable("prod upgrade", utils.DefaultPorts()); err != nil {
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

	// Phase 4: Generate configs (BEFORE service initialization)
	// This ensures node.yaml exists before services try to access it
	if err := setup.Phase4GenerateConfigs(peers, vpsIP, enableHTTPS, domain, joinAddress); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Config generation warning: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Existing configs preserved\n")
	}

	// Phase 2c: Ensure services are properly initialized (fixes existing repos)
	// Now that we have peers and VPS IP, we can properly configure IPFS Cluster
	// Note: IPFS peer info is nil for upgrades - peering is only configured during initial install
	// Note: IPFS Cluster peer info is also nil for upgrades - peer_addresses is only configured during initial install
	fmt.Printf("\nPhase 2c: Ensuring services are properly initialized...\n")
	if err := setup.Phase2cInitializeServices(peers, vpsIP, nil, nil); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service initialization failed: %v\n", err)
		os.Exit(1)
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
		services := utils.GetProductionServices()
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
		active, _ := utils.IsServiceActive(svc)
		status := "❌ Inactive"
		if active {
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
	serviceNames, err := utils.ResolveServiceName(serviceAlias)
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
					fmt.Print("\n" + strings.Repeat("=", 70) + "\n\n")
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

func handleProdStart() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production commands must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Starting all DeBros production services...\n")

	services := utils.GetProductionServices()
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
		masked, err := utils.IsServiceMasked(svc)
		if err == nil && masked {
			fmt.Printf("  ⚠️  %s is masked, unmasking...\n", svc)
			if err := exec.Command("systemctl", "unmask", svc).Run(); err != nil {
				fmt.Printf("  ⚠️  Failed to unmask %s: %v\n", svc, err)
			} else {
				fmt.Printf("  ✓ Unmasked %s\n", svc)
			}
		}

		active, err := utils.IsServiceActive(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check %s: %v\n", svc, err)
			continue
		}
		if active {
			fmt.Printf("  ℹ️  %s already running\n", svc)
			// Re-enable if disabled (in case it was stopped with 'dbn prod stop')
			enabled, err := utils.IsServiceEnabled(svc)
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
	ports, err := utils.CollectPortsForServices(inactive, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
	if err := utils.EnsurePortsAvailable("prod start", ports); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Enable and start inactive services
	for _, svc := range inactive {
		// Re-enable the service first (in case it was disabled by 'dbn prod stop')
		enabled, err := utils.IsServiceEnabled(svc)
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

	fmt.Printf("\n✅ All services started\n")
}

func handleProdStop() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production commands must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Stopping all DeBros production services...\n")

	services := utils.GetProductionServices()
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
		active, err := utils.IsServiceActive(svc)
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
				if stillActive, _ := utils.IsServiceActive(svc); stillActive {
					fmt.Printf("  ❌  %s restarted itself (Restart=always)\n", svc)
					hadError = true
				} else {
					fmt.Printf("  ✓ Stopped %s\n", svc)
				}
			}
		}

		// Disable the service to prevent it from auto-starting on boot
		enabled, err := utils.IsServiceEnabled(svc)
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

	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  ⚠️  No DeBros services found\n")
		return
	}

	// Stop all active services first
	fmt.Printf("  Stopping services...\n")
	for _, svc := range services {
		active, err := utils.IsServiceActive(svc)
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
	ports, err := utils.CollectPortsForServices(services, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
	if err := utils.EnsurePortsAvailable("prod restart", ports); err != nil {
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

	fmt.Printf("\n✅ All services restarted\n")
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
