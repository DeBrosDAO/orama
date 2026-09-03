package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
	"github.com/DeBrosOfficial/network/pkg/constants"

	"context"

	"github.com/DeBrosOfficial/network/pkg/nodehealth"
)

// HandlePostUpgrade brings the node back online after an upgrade:
//  1. Resets failed + unmasks + enables all services
//  2. Starts services in dependency order
//  3. Waits for global RQLite to be ready
//  4. Waits for each namespace RQLite to be ready
//  5. Removes maintenance flag
//
// indexReadyBudget is how long the index rqlite has to rejoin after a restart.
const indexReadyBudget = 2 * time.Minute

func HandlePostUpgrade() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "Error: post-upgrade must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Post-upgrade: bringing node back online...\n")

	// 1. Get all services
	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  Warning: no Orama services found\n")
		return
	}

	// Reset failed state
	resetArgs := []string{"reset-failed"}
	resetArgs = append(resetArgs, services...)
	exec.Command("systemctl", resetArgs...).Run()

	// Unmask and enable all services
	for _, svc := range services {
		masked, err := utils.IsServiceMasked(svc)
		if err == nil && masked {
			exec.Command("systemctl", "unmask", svc).Run()
		}
		enabled, err := utils.IsServiceEnabled(svc)
		if err == nil && !enabled {
			exec.Command("systemctl", "enable", svc).Run()
		}
	}
	fmt.Printf("  Services reset and enabled\n")

	// 2. Start services in dependency order
	fmt.Printf("  Starting services...\n")
	utils.StartServicesOrdered(services, "start")
	fmt.Printf("  Services started\n")

	// 3. Wait for the index RQLite to be ready
	// Fatal. Post-upgrade's job is to bring the node back online, and the
	// index rqlite is what "online" means; warning and carrying on to remove
	// the maintenance flag puts a node that is not serving back into rotation.
	fmt.Printf("  Waiting for index RQLite (port %d)...\n", constants.RQLiteHTTPPort)
	if err := waitForRQLiteReady(constants.RQLiteHTTPPort, indexReadyBudget); err != nil {
		fmt.Fprintf(os.Stderr, "  The index RQLite did not come back: %v\n", err)
		fmt.Fprintf(os.Stderr, "  Leaving the maintenance flag in place — this node is not serving.\n")
		os.Exit(1)
	}
	fmt.Printf("  Global RQLite ready\n")

	// 4. Wait for each namespace RQLite with a global timeout of 5 minutes
	nsPorts := getNamespaceRQLitePorts()
	if len(nsPorts) > 0 {
		fmt.Printf("  Waiting for %d namespace RQLite instances...\n", len(nsPorts))
		globalDeadline := time.Now().Add(5 * time.Minute)

		healthy := 0
		failed := 0
		for ns, port := range nsPorts {
			remaining := time.Until(globalDeadline)
			if remaining <= 0 {
				fmt.Printf("    Warning: global timeout reached, skipping remaining namespaces\n")
				failed += len(nsPorts) - healthy - failed
				break
			}
			timeout := 90 * time.Second
			if remaining < timeout {
				timeout = remaining
			}
			fmt.Printf("    Waiting for namespace '%s' (port %d)...\n", ns, port)
			if err := waitForRQLiteReady(port, timeout); err != nil {
				fmt.Printf("    Warning: namespace '%s' RQLite not ready: %v\n", ns, err)
				failed++
			} else {
				fmt.Printf("    Namespace '%s' ready\n", ns)
				healthy++
			}
		}
		fmt.Printf("  Namespace RQLite: %d healthy, %d failed\n", healthy, failed)
	}

	// 5. Remove maintenance flag
	if err := os.Remove(maintenanceFlagPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("  Warning: failed to remove maintenance flag: %v\n", err)
	} else {
		fmt.Printf("  Maintenance flag removed\n")
	}

	fmt.Printf("Post-upgrade complete. Node is back online.\n")
}

// waitForRQLiteReady waits for an rqlite instance to be carrying its share.
//
// Through nodehealth, which is the one place that decides what "ready" means.
// The hand-rolled poll this replaces accepted any Leader or Follower, so a node
// that rejoined but was tens of thousands of entries behind counted as ready.
//
// No gateway base: this is called per rqlite port, including tenant instances
// that have no gateway of their own. The node's own gateway is checked by the
// upgrade orchestrator's own gate.
func waitForRQLiteReady(port int, timeout time.Duration) error {
	return nodehealth.WaitReady(context.Background(), nodehealth.Target{
		RQLiteBase: fmt.Sprintf("http://localhost:%d", port),
	}, nodehealth.Options{Budget: timeout})
}
