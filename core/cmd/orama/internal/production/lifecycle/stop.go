package lifecycle

import (
	"fmt"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/utils"
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
		{"orama-node"},                       // 1. Stop node (includes gateway + RQLite with leadership transfer)
		{"orama-olric"},                      // 2. Stop cache
		{"orama-ipfs-cluster", "orama-ipfs"}, // 3. Stop storage
		{"orama-vault"},                      // 4. Stop vault
		{"coredns", "caddy"},                 // 5. Stop DNS/TLS last
	}

	// Mask all services to immediately prevent Restart=always from reviving them.
	// Unlike "disable" (which only removes boot symlinks), "mask" links the unit
	// to /dev/null so systemd cannot start it at all. Unmasked by "orama node start".
	maskArgs := []string{"mask"}
	maskArgs = append(maskArgs, services...)
	maskArgs = append(maskArgs, utils.TimerBackingServices(services)...)
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

// namespaceUnitPattern matches every namespace unit instance, timers included.
const namespaceUnitPattern = "orama-namespace-*@*"

// stopAllNamespaceServices stops every namespace unit, its timers first.
func stopAllNamespaceServices() {
	// --plain: without it a failed unit's line starts with a bullet, and the
	// unit was skipped.
	cmd := exec.Command("systemctl", "list-units", "--type=service,timer", "--all", "--plain", "--no-pager", "--no-legend", namespaceUnitPattern)
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("    ⚠️  Warning: Failed to list namespace services: %v\n", err)
		return
	}

	units := namespaceStopOrder(string(output))
	if len(units) == 0 {
		fmt.Printf("    No namespace services found\n")
		return
	}

	for _, unit := range units {
		if err := exec.Command("systemctl", "stop", unit).Run(); err != nil {
			fmt.Printf("    ⚠️  Warning: Failed to stop %s: %v\n", unit, err)
		}
	}

	fmt.Printf("    ✓ Stopped %d namespace unit(s)\n", len(units))
}

// namespaceStopOrder returns the namespace units of a list-units listing,
// timers before services. A timer still running while the services go down can
// start its oneshot in the middle of the stop, and the stop then kills the
// oneshot before it has installed its SIGTERM handler, leaving it failed: on
// stagenet orama-namespace-ipfs-gc@index's timer fell due between the stops of
// ipfs-cluster@index and ipfs@index.
func namespaceStopOrder(listing string) []string {
	var timers, services []string
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "orama-namespace-") {
			continue
		}
		if strings.HasSuffix(fields[0], ".timer") {
			timers = append(timers, fields[0])
		} else {
			services = append(services, fields[0])
		}
	}
	return append(timers, services...)
}
