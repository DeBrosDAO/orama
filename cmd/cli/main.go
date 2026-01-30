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
	case "invite":
		cli.HandleProdCommand(append([]string{"invite"}, args...))
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

	// Deployment commands
	case "deploy":
		cli.HandleDeployCommand(args)
	case "deployments":
		cli.HandleDeploymentsCommand(args)

	// Database commands
	case "db":
		cli.HandleDBCommand(args)

	// Environment management
	case "env":
		cli.HandleEnvCommand(args)

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

	fmt.Printf("📦 Deployments:\n")
	fmt.Printf("  deploy static <path>          - Deploy a static site (React, Vue, etc.)\n")
	fmt.Printf("  deploy nextjs <path>          - Deploy a Next.js application\n")
	fmt.Printf("  deploy go <path>              - Deploy a Go backend\n")
	fmt.Printf("  deploy nodejs <path>          - Deploy a Node.js backend\n")
	fmt.Printf("  deployments list              - List all deployments\n")
	fmt.Printf("  deployments get <name>        - Get deployment details\n")
	fmt.Printf("  deployments logs <name>       - View deployment logs\n")
	fmt.Printf("  deployments delete <name>     - Delete a deployment\n")
	fmt.Printf("  deployments rollback <name>   - Rollback to previous version\n\n")

	fmt.Printf("🗄️  Databases:\n")
	fmt.Printf("  db create <name>              - Create a SQLite database\n")
	fmt.Printf("  db query <name> \"<sql>\"       - Execute SQL query\n")
	fmt.Printf("  db list                       - List all databases\n")
	fmt.Printf("  db backup <name>              - Backup database to IPFS\n")
	fmt.Printf("  db backups <name>             - List database backups\n\n")

	fmt.Printf("🌍 Environments:\n")
	fmt.Printf("  env list                      - List all environments\n")
	fmt.Printf("  env current                   - Show current environment\n")
	fmt.Printf("  env switch <name>             - Switch to environment\n\n")

	fmt.Printf("Global Flags:\n")
	fmt.Printf("  -f, --format <format>         - Output format: table, json (default: table)\n")
	fmt.Printf("  -t, --timeout <duration>      - Operation timeout (default: 30s)\n")
	fmt.Printf("  --help, -h                    - Show this help message\n\n")

	fmt.Printf("Examples:\n")
	fmt.Printf("  # Deploy a React app\n")
	fmt.Printf("  cd my-react-app && npm run build\n")
	fmt.Printf("  orama deploy static ./dist --name my-app\n\n")

	fmt.Printf("  # Deploy a Next.js app with SSR\n")
	fmt.Printf("  cd my-nextjs-app && npm run build\n")
	fmt.Printf("  orama deploy nextjs . --name my-nextjs --ssr\n\n")

	fmt.Printf("  # Create and use a database\n")
	fmt.Printf("  orama db create my-db\n")
	fmt.Printf("  orama db query my-db \"CREATE TABLE users (id INT, name TEXT)\"\n")
	fmt.Printf("  orama db query my-db \"INSERT INTO users VALUES (1, 'Alice')\"\n\n")

	fmt.Printf("  # Manage deployments\n")
	fmt.Printf("  orama deployments list\n")
	fmt.Printf("  orama deployments get my-app\n")
	fmt.Printf("  orama deployments logs my-app --follow\n\n")

	fmt.Printf("  # First node (creates new cluster)\n")
	fmt.Printf("  sudo orama install --vps-ip 203.0.113.1 --domain node-1.orama.network\n\n")

	fmt.Printf("  # Service management\n")
	fmt.Printf("  orama status\n")
	fmt.Printf("  orama logs node --follow\n")
}
