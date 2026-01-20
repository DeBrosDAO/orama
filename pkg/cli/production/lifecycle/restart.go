package lifecycle

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// HandleRestart restarts all production services
func HandleRestart() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production commands must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Restarting all DeBros production services...\n")

	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  ⚠️  No DeBros services found\n")
		return
	}

	// Stop all active services first
	fmt.Printf("  Stopping services...\n")
	for _, svc := range services {
		active, err := utils.IsServiceActive(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check %s: %v\n", svc, err)
			continue
		}
		if !active {
			fmt.Printf("  ℹ️  %s was already stopped\n", svc)
			continue
		}
		if err := exec.Command("systemctl", "stop", svc).Run(); err != nil {
			fmt.Printf("  ⚠️  Failed to stop %s: %v\n", svc, err)
		} else {
			fmt.Printf("  ✓ Stopped %s\n", svc)
		}
	}

	// Check port availability before restarting
	ports, err := utils.CollectPortsForServices(services, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
	if err := utils.EnsurePortsAvailable("prod restart", ports); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Start all services
	fmt.Printf("  Starting services...\n")
	for _, svc := range services {
		if err := exec.Command("systemctl", "start", svc).Run(); err != nil {
			fmt.Printf("  ⚠️  Failed to start %s: %v\n", svc, err)
		} else {
			fmt.Printf("  ✓ Started %s\n", svc)
		}
	}

	fmt.Printf("\n✅ All services restarted\n")
}
