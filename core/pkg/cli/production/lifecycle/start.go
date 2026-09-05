package lifecycle

import (
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"os/exec"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"

	"context"

	"github.com/DeBrosOfficial/network/pkg/constants"

	"github.com/DeBrosOfficial/network/pkg/nodehealth"
)

// HandleStart starts all production services
// startReadyBudget is how long the node has to come up after a start.
const startReadyBudget = 3 * time.Minute

func HandleStart() error {
	if err := clierr.RequireRoot("starting the node services"); err != nil {
		return err
	}

	fmt.Printf("Starting all Orama production services...\n")

	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  ⚠️  No Orama services found\n")
		return nil
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
			// Re-enable if disabled (in case it was stopped with 'orama node stop')
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
		return nil
	}

	// Check port availability for services we're about to start
	ports, err := utils.CollectPortsForServices(inactive, false)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	if err := utils.EnsurePortsAvailable("prod start", ports); err != nil {
		return clierr.Failure("%v", err)
	}

	// Re-enable inactive services first (in case they were disabled by 'orama node stop')
	for _, svc := range inactive {
		enabled, err := utils.IsServiceEnabled(svc)
		if err == nil && !enabled {
			if err := exec.Command("systemctl", "enable", svc).Run(); err != nil {
				fmt.Printf("  ⚠️  Failed to enable %s: %v\n", svc, err)
			} else {
				fmt.Printf("  ✓ Enabled %s (will auto-start on boot)\n", svc)
			}
		}
	}

	// Start services in dependency order (namespace: rqlite → olric → gateway)
	utils.StartServicesOrdered(inactive, "start")

	// Wait for the node to be serving, not for a fixed five seconds. A sleep
	// cannot tell a node that came up in two seconds from one that never came
	// up, and "✅ All services started" printed either way.
	fmt.Printf("  ⏳ Waiting for the node to come up...\n")
	if err := nodehealth.WaitReady(context.Background(), nodehealth.Target{
		RQLiteBase:  fmt.Sprintf("http://localhost:%d", constants.RQLiteHTTPPort),
		GatewayBase: fmt.Sprintf("http://localhost:%d", constants.GatewayAPIPort),
	}, nodehealth.Options{Budget: startReadyBudget}); err != nil {
		return clierr.Failure("Services were started but the node is not serving: %v", err)
	}

	fmt.Printf("\n✅ All services started and the node is serving\n")
	return nil
}
