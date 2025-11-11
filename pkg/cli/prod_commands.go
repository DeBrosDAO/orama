package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	case "status":
		handleProdStatus()
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
	fmt.Printf("      --peers ADDRS         - Comma-separated bootstrap peers (for non-bootstrap)\n")
	fmt.Printf("      --bootstrap-join ADDR - Bootstrap raft join address (for secondary bootstrap)\n")
	fmt.Printf("      --domain DOMAIN       - Domain for HTTPS (optional)\n")
	fmt.Printf("      --branch BRANCH       - Git branch to use (main or nightly, default: main)\n")
	fmt.Printf("  upgrade                   - Upgrade existing installation (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --restart              - Automatically restart services after upgrade\n")
	fmt.Printf("      --branch BRANCH        - Git branch to use (main or nightly, uses saved preference if not specified)\n")
	fmt.Printf("  status                    - Show status of production services\n")
	fmt.Printf("  logs <service>            - View production service logs\n")
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
	fmt.Printf("  sudo dbn prod install --bootstrap --vps-ip 10.0.0.2 --bootstrap-join 10.0.0.1:7001\n\n")
	fmt.Printf("  # Upgrade using saved branch preference\n")
	fmt.Printf("  sudo dbn prod upgrade --restart\n\n")
	fmt.Printf("  # Upgrade and switch to nightly branch\n")
	fmt.Printf("  sudo dbn prod upgrade --restart --branch nightly\n\n")
	fmt.Printf("  dbn prod status\n")
	fmt.Printf("  dbn prod logs node --follow\n")
}

func handleProdInstall(args []string) {
	// Parse arguments
	force := false
	isBootstrap := false
	var vpsIP, domain, peersStr, bootstrapJoin, branch string

	for i, arg := range args {
		switch arg {
		case "--force":
			force = true
		case "--bootstrap":
			isBootstrap = true
		case "--peers":
			if i+1 < len(args) {
				peersStr = args[i+1]
			}
		case "--vps-ip":
			if i+1 < len(args) {
				vpsIP = args[i+1]
			}
		case "--domain":
			if i+1 < len(args) {
				domain = args[i+1]
			}
		case "--bootstrap-join":
			if i+1 < len(args) {
				bootstrapJoin = args[i+1]
			}
		case "--branch":
			if i+1 < len(args) {
				branch = args[i+1]
			}
		}
	}

	// Validate branch if provided
	if branch != "" && branch != "main" && branch != "nightly" {
		fmt.Fprintf(os.Stderr, "❌ Invalid branch: %s (must be 'main' or 'nightly')\n", branch)
		os.Exit(1)
	}

	// Default to main if not specified
	if branch == "" {
		branch = "main"
	}

	// Parse bootstrap peers if provided
	var bootstrapPeers []string
	if peersStr != "" {
		bootstrapPeers = strings.Split(peersStr, ",")
	}

	// Validate setup requirements
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production install must be run as root (use sudo)\n")
		os.Exit(1)
	}

	// Enforce --vps-ip for non-bootstrap nodes
	if !isBootstrap && vpsIP == "" {
		fmt.Fprintf(os.Stderr, "❌ --vps-ip is required for non-bootstrap nodes\n")
		fmt.Fprintf(os.Stderr, "   Usage: sudo dbn prod install --vps-ip <public_ip> --peers <multiaddr>\n")
		os.Exit(1)
	}

	debrosHome := "/home/debros"
	debrosDir := debrosHome + "/.debros"
	setup := production.NewProductionSetup(debrosHome, os.Stdout, force, branch)

	// Save branch preference for future upgrades
	if err := production.SaveBranchPreference(debrosDir, branch); err != nil {
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
	if isBootstrap {
		nodeType = "bootstrap"
	}

	// Phase 3: Generate secrets FIRST (before service initialization)
	// This ensures cluster secret and swarm key exist before repos are seeded
	fmt.Printf("\n🔐 Phase 3: Generating secrets...\n")
	if err := setup.Phase3GenerateSecrets(isBootstrap); err != nil {
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
	enableHTTPS := domain != ""
	if err := setup.Phase4GenerateConfigs(isBootstrap, bootstrapPeers, vpsIP, enableHTTPS, domain, bootstrapJoin); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Configuration generation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 5: Create systemd services
	fmt.Printf("\n🔧 Phase 5: Creating systemd services...\n")
	if err := setup.Phase5CreateSystemdServices(nodeType, vpsIP); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service creation failed: %v\n", err)
		os.Exit(1)
	}

	// Log completion with actual peer ID
	setup.LogSetupComplete(setup.NodePeerID)
	fmt.Printf("✅ Production installation complete!\n\n")
}

func handleProdUpgrade(args []string) {
	// Parse arguments
	force := false
	restartServices := false
	branch := ""
	for i, arg := range args {
		if arg == "--force" {
			force = true
		}
		if arg == "--restart" {
			restartServices = true
		}
		if arg == "--branch" {
			if i+1 < len(args) {
				branch = args[i+1]
			}
		}
	}

	// Validate branch if provided
	if branch != "" && branch != "main" && branch != "nightly" {
		fmt.Fprintf(os.Stderr, "❌ Invalid branch: %s (must be 'main' or 'nightly')\n", branch)
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

	setup := production.NewProductionSetup(debrosHome, os.Stdout, force, branch)

	// If branch was explicitly provided, save it for future upgrades
	if branch != "" {
		if err := production.SaveBranchPreference(debrosDir, branch); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to save branch preference: %v\n", err)
		} else {
			fmt.Printf("  Using branch: %s (saved for future upgrades)\n", branch)
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
	// Note: This will overwrite existing configs, but preserves secrets
	bootstrapPeers := []string{} // Could be read from existing config if needed
	enableHTTPS := false
	domain := ""
	bootstrapJoin := ""
	if err := setup.Phase4GenerateConfigs(nodeType == "bootstrap", bootstrapPeers, "", enableHTTPS, domain, bootstrapJoin); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Config generation warning: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Existing configs preserved\n")
	}

	// Phase 5: Update systemd services
	fmt.Printf("\n🔧 Phase 5: Updating systemd services...\n")
	if err := setup.Phase5CreateSystemdServices(nodeType, ""); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Service update warning: %v\n", err)
	}

	fmt.Printf("\n✅ Upgrade complete!\n")
	if restartServices {
		fmt.Printf("   Restarting services...\n")
		// Reload systemd daemon
		exec.Command("systemctl", "daemon-reload").Run()
		// Restart services to apply changes
		services := []string{
			"debros-ipfs-bootstrap",
			"debros-ipfs-cluster-bootstrap",
			"debros-rqlite-bootstrap",
			"debros-olric",
			"debros-node-bootstrap",
			"debros-gateway",
		}
		for _, svc := range services {
			exec.Command("systemctl", "restart", svc).Run()
		}
		fmt.Printf("   ✓ Services restarted\n")
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
		"debros-rqlite-bootstrap",
		"debros-rqlite-node",
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
		"debros-rqlite-bootstrap":       "RQLite Database (Bootstrap)",
		"debros-rqlite-node":            "RQLite Database (Node)",
		"debros-olric":                  "Olric Cache Server",
		"debros-node-bootstrap":         "DeBros Node (Bootstrap)",
		"debros-node-node":              "DeBros Node (Node)",
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

func handleProdLogs(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: dbn prod logs <service> [--follow]\n")
		os.Exit(1)
	}

	service := args[0]
	follow := false
	if len(args) > 1 && (args[1] == "--follow" || args[1] == "-f") {
		follow = true
	}

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
		"debros-rqlite-bootstrap",
		"debros-rqlite-node",
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
