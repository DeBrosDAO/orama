package logs

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// Handle executes the logs command
func Handle(args []string) {
	if len(args) == 0 {
		showUsage()
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
		handleMultipleServices(serviceNames, serviceAlias, follow)
		return
	}

	// Single service
	service := serviceNames[0]
	if follow {
		followServiceLogs(service)
	} else {
		showServiceLogs(service)
	}
}

func showUsage() {
	fmt.Fprintf(os.Stderr, "Usage: orama prod logs <service> [--follow]\n")
	fmt.Fprintf(os.Stderr, "\nService aliases:\n")
	fmt.Fprintf(os.Stderr, "  node, ipfs, cluster, gateway, olric\n")
	fmt.Fprintf(os.Stderr, "\nOr use full service name:\n")
	fmt.Fprintf(os.Stderr, "  debros-node, debros-gateway, etc.\n")
}

func handleMultipleServices(serviceNames []string, serviceAlias string, follow bool) {
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
}

func followServiceLogs(service string) {
	fmt.Printf("Following logs for %s (press Ctrl+C to stop)...\n\n", service)
	cmd := exec.Command("journalctl", "-u", service, "-f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Run()
}

func showServiceLogs(service string) {
	cmd := exec.Command("journalctl", "-u", service, "-n", "50")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}
