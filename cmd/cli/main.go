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

	// Environment commands
	case "env":
		cli.HandleEnvCommand(args)
	case "devnet", "testnet", "local":
		// Shorthand for switching environments
		if len(args) > 0 && (args[0] == "enable" || args[0] == "switch") {
			if err := cli.SwitchEnvironment(command); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to switch environment: %v\n", err)
				os.Exit(1)
			}
			env, _ := cli.GetActiveEnvironment()
			fmt.Printf("✅ Switched to %s environment\n", command)
			if env != nil {
				fmt.Printf("   Gateway URL: %s\n", env.GatewayURL)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Usage: dbn %s enable\n", command)
			os.Exit(1)
		}

	// Development environment commands
	case "dev":
		cli.HandleDevCommand(args)

	// Production environment commands
	case "prod":
		cli.HandleProdCommand(args)

	// Authentication commands
	case "auth":
		cli.HandleAuthCommand(args)

	// Basic network commands
	case "health":
		cli.HandleHealthCommand(format, timeout)
	case "peers":
		cli.HandlePeersCommand(format, timeout)
	case "status":
		cli.HandleStatusCommand(format, timeout)
	case "peer-id":
		cli.HandlePeerIDCommand(format, timeout)

	// Query command
	case "query":
		if len(args) == 0 {
			fmt.Fprintf(os.Stderr, "Usage: dbn query <sql>\n")
			os.Exit(1)
		}
		cli.HandleQueryCommand(args[0], format, timeout)

	// PubSub commands
	case "pubsub":
		cli.HandlePubSubCommand(args, format, timeout)

	// Connect command
	case "connect":
		if len(args) == 0 {
			fmt.Fprintf(os.Stderr, "Usage: dbn connect <peer_address>\n")
			os.Exit(1)
		}
		cli.HandleConnectCommand(args[0], timeout)

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

	fmt.Printf("🌍 Environment Management:\n")
	fmt.Printf("  env list                      - List available environments\n")
	fmt.Printf("  env current                   - Show current environment\n")
	fmt.Printf("  env switch <env>              - Switch to environment (local, devnet, testnet)\n")
	fmt.Printf("  devnet enable                 - Shorthand for switching to devnet\n")
	fmt.Printf("  testnet enable                - Shorthand for switching to testnet\n\n")

	fmt.Printf("💻 Local Development:\n")
	fmt.Printf("  dev up                        - Start full local dev environment\n")
	fmt.Printf("  dev down                      - Stop all dev services\n")
	fmt.Printf("  dev status                    - Show status of dev services\n")
	fmt.Printf("  dev logs <component>          - View dev component logs\n\n")

	fmt.Printf("🚀 Production Deployment:\n")
	fmt.Printf("  prod install [--bootstrap]    - Full production bootstrap (requires root)\n")
	fmt.Printf("  prod upgrade                  - Upgrade existing installation\n")
	fmt.Printf("  prod status                   - Show production service status\n")
	fmt.Printf("  prod logs <service>           - View production service logs\n")
	fmt.Printf("  prod uninstall                - Remove production services (preserves data)\n\n")

	fmt.Printf("🔐 Authentication:\n")
	fmt.Printf("  auth login                    - Authenticate with wallet\n")
	fmt.Printf("  auth logout                   - Clear stored credentials\n")
	fmt.Printf("  auth whoami                   - Show current authentication\n")
	fmt.Printf("  auth status                   - Show detailed auth info\n\n")

	fmt.Printf("🌐 Network Commands:\n")
	fmt.Printf("  health                        - Check network health\n")
	fmt.Printf("  peers                         - List connected peers\n")
	fmt.Printf("  status                        - Show network status\n")
	fmt.Printf("  peer-id                       - Show this node's peer ID\n")
	fmt.Printf("  connect <peer_address>        - Connect to peer\n\n")

	fmt.Printf("🗄️  Database:\n")
	fmt.Printf("  query <sql>                   🔐 Execute database query\n\n")

	fmt.Printf("📡 PubSub:\n")
	fmt.Printf("  pubsub publish <topic> <msg>  🔐 Publish message\n")
	fmt.Printf("  pubsub subscribe <topic>      🔐 Subscribe to topic\n")
	fmt.Printf("  pubsub topics                 🔐 List topics\n\n")

	fmt.Printf("Global Flags:\n")
	fmt.Printf("  -f, --format <format>         - Output format: table, json (default: table)\n")
	fmt.Printf("  -t, --timeout <duration>      - Operation timeout (default: 30s)\n\n")

	fmt.Printf("🔐 = Requires authentication (auto-prompts if needed)\n\n")

	fmt.Printf("Examples:\n")
	fmt.Printf("  # Switch to devnet\n")
	fmt.Printf("  dbn devnet enable\n\n")

	fmt.Printf("  # Authenticate and query\n")
	fmt.Printf("  dbn auth login\n")
	fmt.Printf("  dbn query \"SELECT * FROM users LIMIT 10\"\n\n")

	fmt.Printf("  # Setup VPS (Linux only)\n")
	fmt.Printf("  sudo dbn setup\n\n")

	fmt.Printf("  # Manage services\n")
	fmt.Printf("  sudo dbn service status all\n")
	fmt.Printf("  sudo dbn service logs node --follow\n")
}
