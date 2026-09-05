package lifecycle

import (
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// HandleStop stops all production services
func HandleStop() error {
	return HandleStopWithFlags(false)
}

// HandleStopForce stops all production services, bypassing quorum checks
func HandleStopForce() error {
	return HandleStopWithFlags(true)
}

// HandleStopWithFlags stops all production services with optional force flag
func HandleStopWithFlags(force bool) error {
	if err := clierr.RequireRoot("stopping the node services"); err != nil {
		return err
	}

	// Pre-flight: check if stopping this node would break RQLite quorum
	if !force {
		if warning := checkQuorumSafety(); warning != "" {
			return clierr.Conflict("%s\n  Use 'orama node stop --force' to proceed anyway.", warning)
		}
	}

	fmt.Printf("Stopping all Orama production services...\n")

	// First, stop all namespace services
	fmt.Printf("\n  Stopping namespace services...\n")
	stopAllNamespaceServices()

	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  No Orama services found\n")
		return nil
	}

	fmt.Printf("\n  Stopping main services (ordered)...\n")

	// Ordered shutdown: node first (supervisor + @index via PartOf), then leftover host units
	shutdownOrder := [][]string{
		{"orama-node"},                                // 1. Stop node (includes gateway + RQLite with leadership transfer)
		{"orama-olric"},                               // 2. Stop cache
		{"orama-ipfs-cluster", "orama-ipfs"},          // 3. Stop storage
		{"orama-vault"},                               // 4. Stop vault
		{"orama-anyone-relay", "orama-anyone-client"}, // 5. Stop privacy relay
		{"coredns", "caddy"},                          // 6. Stop DNS/TLS last
	}

	// Mask all services to immediately prevent Restart=always from reviving them.
	// Unlike "disable" (which only removes boot symlinks), "mask" links the unit
	// to /dev/null so systemd cannot start it at all. Unmasked by "orama node start".
	maskArgs := []string{"mask"}
	maskArgs = append(maskArgs, services...)
	if err := exec.Command("systemctl", maskArgs...).Run(); err != nil {
		fmt.Printf("  Warning: Failed to mask some services: %v\n", err)
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

		// Service is already masked (prevents both restart and boot start).
		// No additional disable needed.
	}

	if hadError {
		fmt.Fprintf(os.Stderr, "\n⚠️  Some services could not be stopped cleanly\n")
		fmt.Fprintf(os.Stderr, "   Check status with: systemctl list-units 'orama-*'\n")
	} else {
		fmt.Printf("\n✅ All services stopped and masked (will not auto-start on boot)\n")
		fmt.Printf("   Use 'orama node start' to unmask and start services\n")
	}
	return nil
}

// stopAllNamespaceServices stops all running namespace services
func stopAllNamespaceServices() {
	// Find all running namespace services using systemctl list-units
	cmd := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-pager", "--no-legend", "orama-namespace-*@*.service")
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
			if strings.HasPrefix(serviceName, "orama-namespace-") {
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
