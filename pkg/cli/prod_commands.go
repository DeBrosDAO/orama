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
	fmt.Printf("      --peers ADDRS         - Comma-separated bootstrap peers (for non-bootstrap)\n")
	fmt.Printf("      --vps-ip IP           - VPS public IP address\n")
	fmt.Printf("      --domain DOMAIN       - Domain for HTTPS (optional)\n")
	fmt.Printf("  upgrade                   - Upgrade existing installation (requires root/sudo)\n")
	fmt.Printf("  status                    - Show status of production services\n")
	fmt.Printf("  logs <service>            - View production service logs\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --follow              - Follow logs in real-time\n")
	fmt.Printf("  uninstall                 - Remove production services (requires root/sudo)\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  sudo dbn prod install --bootstrap\n")
	fmt.Printf("  sudo dbn prod install --peers /ip4/1.2.3.4/tcp/4001/p2p/Qm...\n")
	fmt.Printf("  dbn prod status\n")
	fmt.Printf("  dbn prod logs node --follow\n")
}

func handleProdInstall(args []string) {
	// Parse arguments
	force := false
	isBootstrap := false
	var vpsIP, domain, peersStr string

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
		}
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

	debrosHome := "/home/debros"
	setup := production.NewProductionSetup(debrosHome, os.Stdout, force)

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

	// Phase 2c: Initialize services
	nodeType := "node"
	if isBootstrap {
		nodeType = "bootstrap"
	}
	fmt.Printf("\nPhase 2c: Initializing services...\n")
	if err := setup.Phase2cInitializeServices(nodeType); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service initialization failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 3: Generate secrets
	fmt.Printf("\n🔐 Phase 3: Generating secrets...\n")
	if err := setup.Phase3GenerateSecrets(isBootstrap); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Secret generation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 4: Generate configs
	fmt.Printf("\n⚙️  Phase 4: Generating configurations...\n")
	enableHTTPS := domain != ""
	if err := setup.Phase4GenerateConfigs(isBootstrap, bootstrapPeers, vpsIP, enableHTTPS, domain); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Configuration generation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 5: Create systemd services
	fmt.Printf("\n🔧 Phase 5: Creating systemd services...\n")
	if err := setup.Phase5CreateSystemdServices(nodeType); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service creation failed: %v\n", err)
		os.Exit(1)
	}

	// Log completion
	setup.LogSetupComplete("< peer ID from config >")
	fmt.Printf("✅ Production installation complete!\n\n")
}

func handleProdUpgrade(args []string) {
	// Parse arguments
	force := false
	for _, arg := range args {
		if arg == "--force" {
			force = true
		}
	}

	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production upgrade must be run as root (use sudo)\n")
		os.Exit(1)
	}

	debrosHome := "/home/debros"
	fmt.Printf("🔄 Upgrading production installation...\n")
	fmt.Printf("  This will preserve existing configurations and data\n\n")

	// For now, just re-run the install with force flag
	setup := production.NewProductionSetup(debrosHome, os.Stdout, force)

	if err := setup.Phase1CheckPrerequisites(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Prerequisites check failed: %v\n", err)
		os.Exit(1)
	}

	if err := setup.Phase2ProvisionEnvironment(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Environment provisioning failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Upgrade complete!\n")
	fmt.Printf("   Services will use existing configurations\n")
	fmt.Printf("   To restart services: sudo systemctl restart debros-*\n\n")
}

func handleProdStatus() {
	fmt.Printf("Production Environment Status\n\n")

	servicesList := []struct {
		name string
		desc string
	}{
		{"debros-ipfs-bootstrap", "IPFS Daemon (Bootstrap)"},
		{"debros-ipfs-cluster-bootstrap", "IPFS Cluster (Bootstrap)"},
		{"debros-rqlite-bootstrap", "RQLite Database (Bootstrap)"},
		{"debros-olric", "Olric Cache Server"},
		{"debros-node-bootstrap", "DeBros Node (Bootstrap)"},
		{"debros-gateway", "DeBros Gateway"},
	}

	fmt.Printf("Services:\n")
	for _, svc := range servicesList {
		cmd := "systemctl"
		err := exec.Command(cmd, "is-active", "--quiet", svc.name).Run()
		status := "❌ Inactive"
		if err == nil {
			status = "✅ Active"
		}
		fmt.Printf("  %s: %s\n", status, svc.desc)
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
