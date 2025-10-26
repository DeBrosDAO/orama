package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// HandleServiceCommand handles systemd service management commands
func HandleServiceCommand(args []string) {
	if len(args) == 0 {
		showServiceHelp()
		return
	}

	if runtime.GOOS != "linux" {
		fmt.Fprintf(os.Stderr, "❌ Service commands are only supported on Linux with systemd\n")
		os.Exit(1)
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "start":
		handleServiceStart(subargs)
	case "stop":
		handleServiceStop(subargs)
	case "restart":
		handleServiceRestart(subargs)
	case "status":
		handleServiceStatus(subargs)
	case "logs":
		handleServiceLogs(subargs)
	case "help":
		showServiceHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown service subcommand: %s\n", subcommand)
		showServiceHelp()
		os.Exit(1)
	}
}

func showServiceHelp() {
	fmt.Printf("🔧 Service Management Commands\n\n")
	fmt.Printf("Usage: network-cli service <subcommand> <target> [options]\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  start <target>     - Start services\n")
	fmt.Printf("  stop <target>      - Stop services\n")
	fmt.Printf("  restart <target>   - Restart services\n")
	fmt.Printf("  status <target>    - Show service status\n")
	fmt.Printf("  logs <target>      - View service logs\n\n")
	fmt.Printf("Targets:\n")
	fmt.Printf("  node      - DeBros node service\n")
	fmt.Printf("  gateway   - DeBros gateway service\n")
	fmt.Printf("  all       - All DeBros services\n\n")
	fmt.Printf("Logs Options:\n")
	fmt.Printf("  --follow          - Follow logs in real-time (-f)\n")
	fmt.Printf("  --since=<time>    - Show logs since time (e.g., '1h', '30m', '2d')\n")
	fmt.Printf("  -n <lines>        - Show last N lines\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  network-cli service start node\n")
	fmt.Printf("  network-cli service status all\n")
	fmt.Printf("  network-cli service restart gateway\n")
	fmt.Printf("  network-cli service logs node --follow\n")
	fmt.Printf("  network-cli service logs gateway --since=1h\n")
	fmt.Printf("  network-cli service logs node -n 100\n")
}

func getServices(target string) []string {
	switch target {
	case "node":
		return []string{"debros-node"}
	case "gateway":
		return []string{"debros-gateway"}
	case "all":
		return []string{"debros-node", "debros-gateway"}
	default:
		fmt.Fprintf(os.Stderr, "❌ Invalid target: %s (use: node, gateway, or all)\n", target)
		os.Exit(1)
		return nil
	}
}

func requireRoot() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ This command requires root privileges\n")
		fmt.Fprintf(os.Stderr, "   Run with: sudo network-cli service ...\n")
		os.Exit(1)
	}
}

func handleServiceStart(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: network-cli service start <node|gateway|all>\n")
		os.Exit(1)
	}

	requireRoot()

	target := args[0]
	services := getServices(target)

	fmt.Printf("🚀 Starting services...\n")
	for _, service := range services {
		cmd := exec.Command("systemctl", "start", service)
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to start %s: %v\n", service, err)
			continue
		}
		fmt.Printf("   ✓ Started %s\n", service)
	}
}

func handleServiceStop(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: network-cli service stop <node|gateway|all>\n")
		os.Exit(1)
	}

	requireRoot()

	target := args[0]
	services := getServices(target)

	fmt.Printf("⏹️  Stopping services...\n")
	for _, service := range services {
		cmd := exec.Command("systemctl", "stop", service)
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to stop %s: %v\n", service, err)
			continue
		}
		fmt.Printf("   ✓ Stopped %s\n", service)
	}
}

func handleServiceRestart(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: network-cli service restart <node|gateway|all>\n")
		os.Exit(1)
	}

	requireRoot()

	target := args[0]
	services := getServices(target)

	fmt.Printf("🔄 Restarting services...\n")
	for _, service := range services {
		cmd := exec.Command("systemctl", "restart", service)
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to restart %s: %v\n", service, err)
			continue
		}
		fmt.Printf("   ✓ Restarted %s\n", service)
	}
}

func handleServiceStatus(args []string) {
	if len(args) == 0 {
		args = []string{"all"} // Default to all
	}

	target := args[0]
	services := getServices(target)

	fmt.Printf("📊 Service Status:\n\n")
	for _, service := range services {
		// Use systemctl is-active to get simple status
		cmd := exec.Command("systemctl", "is-active", service)
		output, _ := cmd.Output()
		status := strings.TrimSpace(string(output))

		emoji := "❌"
		if status == "active" {
			emoji = "✅"
		} else if status == "inactive" {
			emoji = "⚪"
		}

		fmt.Printf("%s %s: %s\n", emoji, service, status)

		// Show detailed status
		cmd = exec.Command("systemctl", "status", service, "--no-pager", "-l")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
		fmt.Println()
	}
}

func handleServiceLogs(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: network-cli service logs <node|gateway> [--follow] [--since=<time>] [-n <lines>]\n")
		os.Exit(1)
	}

	target := args[0]
	if target == "all" {
		fmt.Fprintf(os.Stderr, "❌ Cannot show logs for 'all' - specify 'node' or 'gateway'\n")
		os.Exit(1)
	}

	services := getServices(target)
	if len(services) == 0 {
		os.Exit(1)
	}

	service := services[0]

	// Parse options
	journalArgs := []string{"-u", service, "--no-pager"}

	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--follow" || arg == "-f":
			journalArgs = append(journalArgs, "-f")
		case strings.HasPrefix(arg, "--since="):
			since := strings.TrimPrefix(arg, "--since=")
			journalArgs = append(journalArgs, "--since="+since)
		case arg == "-n":
			if i+1 < len(args) {
				journalArgs = append(journalArgs, "-n", args[i+1])
				i++
			}
		}
	}

	fmt.Printf("📜 Logs for %s:\n\n", service)

	cmd := exec.Command("journalctl", journalArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to show logs: %v\n", err)
		os.Exit(1)
	}
}
