package logs

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// Handle executes the logs command
// Run streams logs for one service.
func Run(serviceAlias string, follow bool) error {
	// Resolve service alias to actual service names
	serviceNames, err := utils.ResolveServiceName(serviceAlias)
	if err != nil {
		return fmt.Errorf("%w\n\nAvailable service aliases: node, ipfs, cluster, gateway, olric\nOr use a full service name like: orama-node", err)
	}

	// If multiple services match, show all of them
	if len(serviceNames) > 1 {
		handleMultipleServices(serviceNames, serviceAlias, follow)
		return nil
	}

	// Single service
	service := serviceNames[0]
	if follow {
		followServiceLogs(service)
	} else {
		showServiceLogs(service)
	}
	return nil
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
