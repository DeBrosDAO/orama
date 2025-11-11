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
		fmt.Printf("dbn %s", version)
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

	// Production environment commands
	case "prod":
		cli.HandleProdCommand(args)

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
	fmt.Printf("Network CLI - Distributed P2P Network Management Tool\n\n")
	fmt.Printf("Usage: dbn <command> [args...]\n\n")

	fmt.Printf("💻 Local Development:\n")
	fmt.Printf("  dev up                        - Start full local dev environment\n")
	fmt.Printf("  dev down                      - Stop all dev services\n")
	fmt.Printf("  dev status                    - Show status of dev services\n")
	fmt.Printf("  dev logs <component>          - View dev component logs\n")
	fmt.Printf("  dev help                      - Show dev command help\n\n")

	fmt.Printf("🚀 Production Deployment:\n")
	fmt.Printf("  prod install [--bootstrap]    - Full production bootstrap (requires root/sudo)\n")
	fmt.Printf("  prod upgrade                  - Upgrade existing installation\n")
	fmt.Printf("  prod status                   - Show production service status\n")
	fmt.Printf("  prod start                    - Start all production services (requires root/sudo)\n")
	fmt.Printf("  prod stop                     - Stop all production services (requires root/sudo)\n")
	fmt.Printf("  prod restart                  - Restart all production services (requires root/sudo)\n")
	fmt.Printf("  prod logs <service>           - View production service logs\n")
	fmt.Printf("  prod uninstall                - Remove production services (requires root/sudo)\n")
	fmt.Printf("  prod help                     - Show prod command help\n\n")

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
	fmt.Printf("  # Authenticate\n")
	fmt.Printf("  dbn auth login\n\n")

	fmt.Printf("  # Start local dev environment\n")
	fmt.Printf("  dbn dev up\n")
	fmt.Printf("  dbn dev status\n\n")

	fmt.Printf("  # Production deployment (requires root/sudo)\n")
	fmt.Printf("  sudo dbn prod install --bootstrap\n")
	fmt.Printf("  sudo dbn prod upgrade\n")
	fmt.Printf("  dbn prod status\n")
	fmt.Printf("  dbn prod logs node --follow\n")
}
