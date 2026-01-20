package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// HandleStop stops all production services
func HandleStop() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production commands must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Stopping all DeBros production services...\n")

	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  ⚠️  No DeBros services found\n")
		return
	}

	// First, disable all services to prevent auto-restart
	disableArgs := []string{"disable"}
	disableArgs = append(disableArgs, services...)
	if err := exec.Command("systemctl", disableArgs...).Run(); err != nil {
		fmt.Printf("  ⚠️  Warning: Failed to disable some services: %v\n", err)
	}

	// Stop all services at once using a single systemctl command
	// This is more efficient and ensures they all stop together
	stopArgs := []string{"stop"}
	stopArgs = append(stopArgs, services...)
	if err := exec.Command("systemctl", stopArgs...).Run(); err != nil {
		fmt.Printf("  ⚠️  Warning: Some services may have failed to stop: %v\n", err)
		// Continue anyway - we'll verify and handle individually below
	}

	// Wait a moment for services to fully stop
	time.Sleep(2 * time.Second)

	// Reset failed state for any services that might be in failed state
	resetArgs := []string{"reset-failed"}
	resetArgs = append(resetArgs, services...)
	exec.Command("systemctl", resetArgs...).Run()

	// Wait again after reset-failed
	time.Sleep(1 * time.Second)

	// Stop again to ensure they're stopped
	exec.Command("systemctl", stopArgs...).Run()
	time.Sleep(1 * time.Second)

	hadError := false
	for _, svc := range services {
		active, err := utils.IsServiceActive(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check %s: %v\n", svc, err)
			hadError = true
			continue
		}
		if !active {
			fmt.Printf("  ✓ Stopped %s\n", svc)
		} else {
			// Service is still active, try stopping it individually
			fmt.Printf("  ⚠️  %s still active, attempting individual stop...\n", svc)
			if err := exec.Command("systemctl", "stop", svc).Run(); err != nil {
				fmt.Printf("  ❌  Failed to stop %s: %v\n", svc, err)
				hadError = true
			} else {
				// Wait and verify again
				time.Sleep(1 * time.Second)
				if stillActive, _ := utils.IsServiceActive(svc); stillActive {
					fmt.Printf("  ❌  %s restarted itself (Restart=always)\n", svc)
					hadError = true
				} else {
					fmt.Printf("  ✓ Stopped %s\n", svc)
				}
			}
		}

		// Disable the service to prevent it from auto-starting on boot
		enabled, err := utils.IsServiceEnabled(svc)
		if err != nil {
			fmt.Printf("  ⚠️  Unable to check if %s is enabled: %v\n", svc, err)
			// Continue anyway - try to disable
		}
		if enabled {
			if err := exec.Command("systemctl", "disable", svc).Run(); err != nil {
				fmt.Printf("  ⚠️  Failed to disable %s: %v\n", svc, err)
				hadError = true
			} else {
				fmt.Printf("  ✓ Disabled %s (will not auto-start on boot)\n", svc)
			}
		} else {
			fmt.Printf("  ℹ️  %s already disabled\n", svc)
		}
	}

	if hadError {
		fmt.Fprintf(os.Stderr, "\n⚠️  Some services may still be restarting due to Restart=always\n")
		fmt.Fprintf(os.Stderr, "   Check status with: systemctl list-units 'debros-*'\n")
		fmt.Fprintf(os.Stderr, "   If services are still restarting, they may need manual intervention\n")
	} else {
		fmt.Printf("\n✅ All services stopped and disabled (will not auto-start on boot)\n")
		fmt.Printf("   Use 'dbn prod start' to start and re-enable services\n")
	}
}
