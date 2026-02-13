package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// HandleStop stops all production services
func HandleStop() {
	HandleStopWithFlags(false)
}

// HandleStopForce stops all production services, bypassing quorum checks
func HandleStopForce() {
	HandleStopWithFlags(true)
}

// HandleStopWithFlags stops all production services with optional force flag
func HandleStopWithFlags(force bool) {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "Error: Production commands must be run as root (use sudo)\n")
		os.Exit(1)
	}

	// Pre-flight: check if stopping this node would break RQLite quorum
	if !force {
		if warning := checkQuorumSafety(); warning != "" {
			fmt.Fprintf(os.Stderr, "\nWARNING: %s\n", warning)
			fmt.Fprintf(os.Stderr, "Use 'orama prod stop --force' to proceed anyway.\n\n")
			os.Exit(1)
		}
	}

	fmt.Printf("Stopping all DeBros production services...\n")

	// First, stop all namespace services
	fmt.Printf("\n  Stopping namespace services...\n")
	stopAllNamespaceServices()

	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  No DeBros services found\n")
		return
	}

	fmt.Printf("\n  Stopping main services (ordered)...\n")

	// Ordered shutdown: gateway first, then node (RQLite), then supporting services
	// This ensures we stop accepting requests before shutting down the database
	shutdownOrder := [][]string{
		{"debros-gateway"},                         // 1. Stop accepting new requests
		{"debros-node"},                            // 2. Stop node (includes RQLite with leadership transfer)
		{"debros-olric"},                           // 3. Stop cache
		{"debros-ipfs-cluster", "debros-ipfs"},     // 4. Stop storage
		{"debros-anyone-relay", "anyone-client"},   // 5. Stop privacy relay
		{"coredns", "caddy"},                       // 6. Stop DNS/TLS last
	}

	// First, disable all services to prevent auto-restart
	disableArgs := []string{"disable"}
	disableArgs = append(disableArgs, services...)
	if err := exec.Command("systemctl", disableArgs...).Run(); err != nil {
		fmt.Printf("  Warning: Failed to disable some services: %v\n", err)
	}

	// Stop services in order with brief pauses between groups
	for _, group := range shutdownOrder {
		for _, svc := range group {
			if !containsService(services, svc) {
				continue
			}
			if err := exec.Command("systemctl", "stop", svc).Run(); err != nil {
				// Not all services may exist on all nodes
			} else {
				fmt.Printf("  Stopped %s\n", svc)
			}
		}
		time.Sleep(2 * time.Second) // Brief pause between groups for drain
	}

	// Stop any remaining services not in the ordered list
	remainingStopArgs := []string{"stop"}
	remainingStopArgs = append(remainingStopArgs, services...)
	_ = exec.Command("systemctl", remainingStopArgs...).Run()

	// Wait a moment for services to fully stop
	time.Sleep(2 * time.Second)

	// Reset failed state for any services that might be in failed state
	resetArgs := []string{"reset-failed"}
	resetArgs = append(resetArgs, services...)
	if err := exec.Command("systemctl", resetArgs...).Run(); err != nil {
		fmt.Printf("  ⚠️  Warning: Failed to reset-failed state: %v\n", err)
	}

	// Wait again after reset-failed
	time.Sleep(1 * time.Second)

	// Stop again to ensure they're stopped
	secondStopArgs := []string{"stop"}
	secondStopArgs = append(secondStopArgs, services...)
	if err := exec.Command("systemctl", secondStopArgs...).Run(); err != nil {
		fmt.Printf("  ⚠️  Warning: Second stop attempt had errors: %v\n", err)
	}
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
		fmt.Printf("   Use 'orama prod start' to start and re-enable services\n")
	}
}

// stopAllNamespaceServices stops all running namespace services
func stopAllNamespaceServices() {
	// Find all running namespace services using systemctl list-units
	cmd := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-pager", "--no-legend", "debros-namespace-*@*.service")
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("    ⚠️  Warning: Failed to list namespace services: %v\n", err)
		return
	}

	lines := strings.Split(string(output), "\n")
	var namespaceServices []string
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			serviceName := fields[0]
			if strings.HasPrefix(serviceName, "debros-namespace-") {
				namespaceServices = append(namespaceServices, serviceName)
			}
		}
	}

	if len(namespaceServices) == 0 {
		fmt.Printf("    No namespace services found\n")
		return
	}

	// Stop all namespace services
	for _, svc := range namespaceServices {
		if err := exec.Command("systemctl", "stop", svc).Run(); err != nil {
			fmt.Printf("    ⚠️  Warning: Failed to stop %s: %v\n", svc, err)
		}
	}

	fmt.Printf("    ✓ Stopped %d namespace service(s)\n", len(namespaceServices))
}
