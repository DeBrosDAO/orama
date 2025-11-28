package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/environments/development"
)

// HandleDevCommand handles the dev command group
func HandleDevCommand(args []string) {
	if len(args) == 0 {
		showDevHelp()
		return
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "up":
		handleDevUp(subargs)
	case "down":
		handleDevDown(subargs)
	case "status":
		handleDevStatus(subargs)
	case "logs":
		handleDevLogs(subargs)
	case "help":
		showDevHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown dev subcommand: %s\n", subcommand)
		showDevHelp()
		os.Exit(1)
	}
}

func showDevHelp() {
	fmt.Printf("🚀 Development Environment Commands\n\n")
	fmt.Printf("Usage: orama dev <subcommand> [options]\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  up                - Start development environment (5 nodes + gateway)\n")
	fmt.Printf("  down              - Stop all development services\n")
	fmt.Printf("  status            - Show status of running services\n")
	fmt.Printf("  logs <component>  - Tail logs for a component\n")
	fmt.Printf("  help              - Show this help\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  orama dev up\n")
	fmt.Printf("  orama dev down\n")
	fmt.Printf("  orama dev status\n")
	fmt.Printf("  orama dev logs node-1 --follow\n")
}

func handleDevUp(args []string) {
	ctx := context.Background()

	// Get home directory and .orama path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	oramaDir := filepath.Join(homeDir, ".orama")

	// Step 1: Check dependencies
	fmt.Printf("📋 Checking dependencies...\n\n")
	checker := development.NewDependencyChecker()
	if _, err := checker.CheckAll(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ All required dependencies available\n\n")

	// Step 2: Check ports
	fmt.Printf("🔌 Checking port availability...\n\n")
	portChecker := development.NewPortChecker()
	if _, err := portChecker.CheckAll(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n\n", err)
		fmt.Fprintf(os.Stderr, "Port mapping:\n")
		for port, service := range development.PortMap() {
			fmt.Fprintf(os.Stderr, "  %d - %s\n", port, service)
		}
		fmt.Fprintf(os.Stderr, "\n")
		os.Exit(1)
	}
	fmt.Printf("✓ All required ports available\n\n")

	// Step 3: Ensure configs
	fmt.Printf("⚙️  Preparing configuration files...\n\n")
	ensurer := development.NewConfigEnsurer(oramaDir)
	if err := ensurer.EnsureAll(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to prepare configs: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n")

	// Step 4: Start services
	pm := development.NewProcessManager(oramaDir, os.Stdout)
	if err := pm.StartAll(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error starting services: %v\n", err)
		os.Exit(1)
	}

	// Step 5: Show summary
	fmt.Printf("🎉 Development environment is running!\n\n")
	fmt.Printf("Key endpoints:\n")
	fmt.Printf("  Gateway:              http://localhost:6001\n")
	fmt.Printf("  Node-1 IPFS:          http://localhost:4501\n")
	fmt.Printf("  Node-2 IPFS:          http://localhost:4502\n")
	fmt.Printf("  Node-3 IPFS:          http://localhost:4503\n")
	fmt.Printf("  Node-4 IPFS:          http://localhost:4504\n")
	fmt.Printf("  Node-5 IPFS:          http://localhost:4505\n")
	fmt.Printf("  Anon SOCKS:           127.0.0.1:9050\n")
	fmt.Printf("  Olric Cache:          http://localhost:3320\n\n")
	fmt.Printf("Useful commands:\n")
	fmt.Printf("  orama dev status       - Show status\n")
	fmt.Printf("  orama dev logs node-1  - Node-1 logs\n")
	fmt.Printf("  orama dev logs node-2  - Node-2 logs\n")
	fmt.Printf("  orama dev down         - Stop all services\n\n")
	fmt.Printf("Logs directory: %s/logs\n\n", oramaDir)
}

func handleDevDown(args []string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	oramaDir := filepath.Join(homeDir, ".orama")

	pm := development.NewProcessManager(oramaDir, os.Stdout)
	ctx := context.Background()

	if err := pm.StopAll(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Error stopping services: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ All services have been stopped\n\n")
}

func handleDevStatus(args []string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	oramaDir := filepath.Join(homeDir, ".orama")

	pm := development.NewProcessManager(oramaDir, os.Stdout)
	ctx := context.Background()

	pm.Status(ctx)
}

func handleDevLogs(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: dbn dev logs <component> [--follow]\n")
		fmt.Fprintf(os.Stderr, "\nComponents: node-1, node-2, node-3, node-4, node-5, gateway, ipfs-node-1, ipfs-node-2, ipfs-node-3, ipfs-node-4, ipfs-node-5, olric, anon\n")
		os.Exit(1)
	}

	component := args[0]
	follow := len(args) > 1 && args[1] == "--follow"

	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	oramaDir := filepath.Join(homeDir, ".orama")

	logPath := filepath.Join(oramaDir, "logs", fmt.Sprintf("%s.log", component))
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "❌ Log file not found: %s\n", logPath)
		os.Exit(1)
	}

	if follow {
		// Run tail -f
		tailCmd := fmt.Sprintf("tail -f %s", logPath)
		fmt.Printf("Following %s (press Ctrl+C to stop)...\n\n", logPath)
		// syscall.Exec doesn't work in all environments, use exec.Command instead
		cmd := exec.Command("sh", "-c", tailCmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Run()
	} else {
		// Cat the file
		data, _ := os.ReadFile(logPath)
		fmt.Print(string(data))
	}
}
