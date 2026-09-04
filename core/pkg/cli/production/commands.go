package production

import (
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/pkg/cli/production/install"
	"github.com/DeBrosOfficial/network/pkg/cli/production/invite"
	"github.com/DeBrosOfficial/network/pkg/cli/production/lifecycle"
	"github.com/DeBrosOfficial/network/pkg/cli/production/logs"
	"github.com/DeBrosOfficial/network/pkg/cli/production/migrate"
	"github.com/DeBrosOfficial/network/pkg/cli/production/status"
	"github.com/DeBrosOfficial/network/pkg/cli/production/uninstall"
	"github.com/DeBrosOfficial/network/pkg/cli/production/upgrade"
)

// HandleCommand handles production environment commands
func HandleCommand(args []string) {
	if len(args) == 0 {
		ShowHelp()
		return
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "invite":
		invite.Handle(subargs)
	case "install":
		install.Handle(subargs)
	case "upgrade":
		upgrade.Handle(subargs)
	case "migrate":
		migrate.Handle(subargs)
	case "status":
		status.Handle()
	case "start":
		lifecycle.HandleStart()
	case "stop":
		force := hasFlag(subargs, "--force")
		lifecycle.HandleStopWithFlags(force)
	case "restart":
		force := hasFlag(subargs, "--force")
		lifecycle.HandleRestartWithFlags(force)
	case "pre-upgrade":
		lifecycle.HandlePreUpgrade()
	case "post-upgrade":
		lifecycle.HandlePostUpgrade()
	case "logs":
		logs.Handle(subargs)
	case "uninstall":
		uninstall.Handle()
	case "help":
		ShowHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown prod subcommand: %s\n", subcommand)
		ShowHelp()
		os.Exit(1)
	}
}

// hasFlag checks if a flag is present in the args slice
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// ShowHelp displays help information for production commands
func ShowHelp() {
	fmt.Printf("Production Environment Commands\n\n")
	fmt.Printf("Usage: orama <subcommand> [options]\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  install                   - Install production node (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --interactive         - Launch interactive TUI wizard\n")
	fmt.Printf("      --force               - Reconfigure all settings\n")
	fmt.Printf("      --vps-ip IP           - VPS public IP address (required)\n")
	fmt.Printf("      --domain DOMAIN       - Domain for HTTPS (auto-generated if omitted)\n")
	fmt.Printf("      --peers ADDRS         - Comma-separated peer multiaddrs (for joining cluster)\n")
	fmt.Printf("      --join ADDR           - RQLite join address IP:port (for joining cluster)\n")
	fmt.Printf("      --cluster-secret HEX  - 64-hex cluster secret (required when joining)\n")
	fmt.Printf("      --swarm-key HEX       - 64-hex IPFS swarm key (required when joining)\n")
	fmt.Printf("      --ipfs-peer ID        - IPFS peer ID to connect to (auto-discovered)\n")
	fmt.Printf("      --ipfs-addrs ADDRS    - IPFS swarm addresses (auto-discovered)\n")
	fmt.Printf("      --ipfs-cluster-peer ID - IPFS Cluster peer ID (auto-discovered)\n")
	fmt.Printf("      --ipfs-cluster-addrs ADDRS - IPFS Cluster addresses (auto-discovered)\n")
	fmt.Printf("      --ignore-resource-checks - Skip disk/RAM/CPU prerequisite validation\n")
	fmt.Printf("      --dry-run             - Show what would be done without making changes\n")
	fmt.Printf("  upgrade                   - Upgrade existing installation (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --restart              - Automatically restart services after upgrade\n")
	fmt.Printf("  migrate                   - Migrate from old unified setup (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --dry-run              - Show what would be migrated without making changes\n")
	fmt.Printf("  status                    - Show status of production services\n")
	fmt.Printf("  start                     - Start all production services (requires root/sudo)\n")
	fmt.Printf("  stop                      - Stop all production services (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --force               - Bypass quorum safety check\n")
	fmt.Printf("  restart                   - Restart all production services (requires root/sudo)\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --force               - Bypass quorum safety check\n")
	fmt.Printf("  pre-upgrade               - Prepare node for safe restart (requires root/sudo)\n")
	fmt.Printf("                              Transfers leadership, enters maintenance mode, waits for propagation\n")
	fmt.Printf("  post-upgrade              - Bring node back online after restart (requires root/sudo)\n")
	fmt.Printf("                              Starts services, verifies RQLite health, exits maintenance\n")
	fmt.Printf("  logs <service>            - View production service logs\n")
	fmt.Printf("    Service aliases: node, ipfs, cluster, gateway, olric\n")
	fmt.Printf("    Options:\n")
	fmt.Printf("      --follow              - Follow logs in real-time\n")
	fmt.Printf("  uninstall                 - Remove production services (requires root/sudo)\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  # First node (creates new cluster)\n")
	fmt.Printf("  sudo orama node install --vps-ip 203.0.113.1 --domain node-1.orama.network\n\n")
	fmt.Printf("  # Join existing cluster\n")
	fmt.Printf("  sudo orama node install --vps-ip 203.0.113.2 --domain node-2.orama.network \\\n")
	fmt.Printf("    --peers /ip4/203.0.113.1/tcp/4001/p2p/12D3KooW... \\\n")
	fmt.Printf("    --cluster-secret <64-hex-secret> --swarm-key <64-hex-swarm-key>\n\n")
	fmt.Printf("  # Upgrade\n")
	fmt.Printf("  sudo orama upgrade --restart\n\n")
	fmt.Printf("  # Service management\n")
	fmt.Printf("  sudo orama start\n")
	fmt.Printf("  sudo orama stop\n")
	fmt.Printf("  sudo orama restart\n\n")
	fmt.Printf("  orama status\n")
	fmt.Printf("  orama node logs node --follow\n")
}
