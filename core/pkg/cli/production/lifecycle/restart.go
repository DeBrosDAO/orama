package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// HandleRestart restarts all production services
func HandleRestart() {
	HandleRestartWithFlags(false)
}

// HandleRestartForce restarts all production services, bypassing quorum checks
func HandleRestartForce() {
	HandleRestartWithFlags(true)
}

// HandleRestartWithFlags restarts all production services with optional force flag
func HandleRestartWithFlags(force bool) {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "Error: Production commands must be run as root (use sudo)\n")
		os.Exit(1)
	}

	// Pre-flight: check if restarting this node would temporarily break quorum
	if !force {
		if warning := checkQuorumSafety(); warning != "" {
			fmt.Fprintf(os.Stderr, "\nWARNING: %s\n", warning)
			fmt.Fprintf(os.Stderr, "Use 'orama node restart --force' to proceed anyway.\n\n")
			os.Exit(1)
		}
	}

	fmt.Printf("Restarting all Orama production services...\n")

	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  No Orama services found\n")
		return
	}

	// Stop namespace services first (same as stop command)
	fmt.Printf("\n  Stopping namespace services...\n")
	stopAllNamespaceServices()

	// Ordered stop: node first (includes embedded gateway + RQLite), then supporting services
	fmt.Printf("\n  Stopping services (ordered)...\n")
	shutdownOrder := [][]string{
		{"orama-node"},
		{"orama-olric"},
		{"orama-ipfs-cluster", "orama-ipfs"},
		{"orama-vault"},
		{"orama-anyone-relay", "orama-anyone-client"},
		{"coredns", "caddy"},
	}

	for _, group := range shutdownOrder {
		for _, svc := range group {
			if !containsService(services, svc) {
				continue
			}
			active, _ := utils.IsServiceActive(svc)
			if !active {
				fmt.Printf("  %s was already stopped\n", svc)
				continue
			}
			if err := exec.Command("systemctl", "stop", svc).Run(); err != nil {
				fmt.Printf("  Warning: Failed to stop %s: %v\n", svc, err)
			} else {
				fmt.Printf("  Stopped %s\n", svc)
			}
		}
		time.Sleep(1 * time.Second)
	}

	// Stop any remaining services not in the ordered list
	for _, svc := range services {
		active, _ := utils.IsServiceActive(svc)
		if active {
			_ = exec.Command("systemctl", "stop", svc).Run()
		}
	}

	// Check port availability before restarting
	ports, err := utils.CollectPortsForServices(services, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := utils.EnsurePortsAvailable("prod restart", ports); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Start all services in dependency order
	fmt.Printf("\n  Starting services...\n")
	utils.StartServicesOrdered(services, "start")

	fmt.Printf("\n All services restarted\n")
}
