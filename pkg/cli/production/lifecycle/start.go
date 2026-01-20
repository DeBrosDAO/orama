package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// HandleStart starts all production services
func HandleStart() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production commands must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Starting all DeBros production services...\n")

	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  ⚠️  No DeBros services found\n")
		return
	}

	// Reset failed state for all services before starting
	// This helps with services that were previously in failed state
	resetArgs := []string{"reset-failed"}
	resetArgs = append(resetArgs, services...)
	exec.Command("systemctl", resetArgs...).Run()

	// Check which services are inactive and need to be started
	inactive := make([]string, 0, len(services))
	for _, svc := range services {
		// Check if service is masked and unmask it
		masked, err := utils.IsServiceMasked(svc)
		if err == nil && masked {
			fmt.Printf("  ⚠️  %s is masked, unmasking...\n", svc)
			if err := exec.Command("systemctl", "unmask", svc).Run(); err != nil {
				fmt.Printf("  ⚠️  Failed to unmask %s: %v\n", svc, err)
			} else {
				fmt.Printf("  ✓ Unmasked %s\n", svc)
			}
		}

		active, err := utils.IsServiceActive(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check %s: %v\n", svc, err)
			continue
		}
		if active {
			fmt.Printf("  ℹ️  %s already running\n", svc)
			// Re-enable if disabled (in case it was stopped with 'dbn prod stop')
			enabled, err := utils.IsServiceEnabled(svc)
			if err == nil && !enabled {
				if err := exec.Command("systemctl", "enable", svc).Run(); err != nil {
					fmt.Printf("  ⚠️  Failed to re-enable %s: %v\n", svc, err)
				} else {
					fmt.Printf("  ✓ Re-enabled %s (will auto-start on boot)\n", svc)
				}
			}
			continue
		}
		inactive = append(inactive, svc)
	}

	if len(inactive) == 0 {
		fmt.Printf("\n✅ All services already running\n")
		return
	}

	// Check port availability for services we're about to start
	ports, err := utils.CollectPortsForServices(inactive, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
	if err := utils.EnsurePortsAvailable("prod start", ports); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Enable and start inactive services
	for _, svc := range inactive {
		// Re-enable the service first (in case it was disabled by 'dbn prod stop')
		enabled, err := utils.IsServiceEnabled(svc)
		if err == nil && !enabled {
			if err := exec.Command("systemctl", "enable", svc).Run(); err != nil {
				fmt.Printf("  ⚠️  Failed to enable %s: %v\n", svc, err)
			} else {
				fmt.Printf("  ✓ Enabled %s (will auto-start on boot)\n", svc)
			}
		}

		// Start the service
		if err := exec.Command("systemctl", "start", svc).Run(); err != nil {
			fmt.Printf("  ⚠️  Failed to start %s: %v\n", svc, err)
		} else {
			fmt.Printf("  ✓ Started %s\n", svc)
		}
	}

	// Give services more time to fully initialize before verification
	// Some services may need more time to start up, especially if they're
	// waiting for dependencies or initializing databases
	fmt.Printf("  ⏳ Waiting for services to initialize...\n")
	time.Sleep(5 * time.Second)

	fmt.Printf("\n✅ All services started\n")
}
