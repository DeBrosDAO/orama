package main

import (
	"fmt"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli"
)

var (
	timeout = 30 * time.Second
	format  = "table"
)

// version metadata populated via -ldflags at build time
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	if len(os.Args) < 2 {
		showHelp()
		return
	}

	command := os.Args[1]
	args := os.Args[2:]

	// Parse global flags
	parseGlobalFlags(args)

	switch command {
	case "version":
		fmt.Printf("orama %s", version)
		if commit != "" {
			fmt.Printf(" (commit %s)", commit)
		}
		if date != "" {
			fmt.Printf(" built %s", date)
		}
		fmt.Println()
		return

	// Development environment commands
	case "dev":
		cli.HandleDevCommand(args)

	// Production environment commands (legacy with 'prod' prefix)
	case "prod":
		cli.HandleProdCommand(args)

	// Direct production commands (new simplified interface)
	case "install":
		cli.HandleProdCommand(append([]string{"install"}, args...))
	case "upgrade":
		cli.HandleProdCommand(append([]string{"upgrade"}, args...))
	case "migrate":
		cli.HandleProdCommand(append([]string{"migrate"}, args...))
	case "status":
		cli.HandleProdCommand(append([]string{"status"}, args...))
	case "start":
		cli.HandleProdCommand(append([]string{"start"}, args...))
	case "stop":
		cli.HandleProdCommand(append([]string{"stop"}, args...))
	case "restart":
		cli.HandleProdCommand(append([]string{"restart"}, args...))
	case "logs":
		cli.HandleProdCommand(append([]string{"logs"}, args...))
	case "uninstall":
		cli.HandleProdCommand(append([]string{"uninstall"}, args...))

	// Authentication commands
	case "auth":
		cli.HandleAuthCommand(args)

	// Help
	case "help", "--help", "-h":
		showHelp()

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		showHelp()
		os.Exit(1)
	}
}

func parseGlobalFlags(args []string) {
	for i, arg := range args {
		switch arg {
		case "-f", "--format":
			if i+1 < len(args) {
				format = args[i+1]
			}
		case "-t", "--timeout":
			if i+1 < len(args) {
				if d, err := time.ParseDuration(args[i+1]); err == nil {
					timeout = d
				}
			}
		}
	}
}

func showHelp() {
	fmt.Printf("Orama CLI - Distributed P2P Network Management Tool\n\n")
	fmt.Printf("Usage: orama <command> [args...]\n\n")

	fmt.Printf("💻 Local Development:\n")
	fmt.Printf("  dev up                        - Start full local dev environment\n")
	fmt.Printf("  dev down                      - Stop all dev services\n")
	fmt.Printf("  dev status                    - Show status of dev services\n")
	fmt.Printf("  dev logs <component>          - View dev component logs\n")
	fmt.Printf("  dev help                      - Show dev command help\n\n")

	fmt.Printf("🚀 Production Deployment:\n")
	fmt.Printf("  install                       - Install production node (requires root/sudo)\n")
	fmt.Printf("  upgrade                       - Upgrade existing installation\n")
	fmt.Printf("  status                        - Show production service status\n")
	fmt.Printf("  start                         - Start all production services (requires root/sudo)\n")
	fmt.Printf("  stop                          - Stop all production services (requires root/sudo)\n")
	fmt.Printf("  restart                       - Restart all production services (requires root/sudo)\n")
	fmt.Printf("  logs <service>                - View production service logs\n")
	fmt.Printf("  uninstall                     - Remove production services (requires root/sudo)\n\n")

	fmt.Printf("🔐 Authentication:\n")
	fmt.Printf("  auth login                    - Authenticate with wallet\n")
	fmt.Printf("  auth logout                   - Clear stored credentials\n")
	fmt.Printf("  auth whoami                   - Show current authentication\n")
	fmt.Printf("  auth status                   - Show detailed auth info\n")
	fmt.Printf("  auth help                     - Show auth command help\n\n")

	fmt.Printf("Global Flags:\n")
	fmt.Printf("  -f, --format <format>         - Output format: table, json (default: table)\n")
	fmt.Printf("  -t, --timeout <duration>      - Operation timeout (default: 30s)\n")
	fmt.Printf("  --help, -h                    - Show this help message\n\n")

	fmt.Printf("Examples:\n")
	fmt.Printf("  # First node (creates new cluster)\n")
	fmt.Printf("  sudo orama install --vps-ip 203.0.113.1 --domain node-1.orama.network\n\n")

	fmt.Printf("  # Join existing cluster\n")
	fmt.Printf("  sudo orama install --vps-ip 203.0.113.2 --domain node-2.orama.network \\\n")
	fmt.Printf("    --peers /ip4/203.0.113.1/tcp/4001/p2p/12D3KooW... --cluster-secret <hex>\n\n")

	fmt.Printf("  # Service management\n")
	fmt.Printf("  orama status\n")
	fmt.Printf("  orama logs node --follow\n")
}
