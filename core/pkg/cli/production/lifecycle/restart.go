package lifecycle

import (
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"os/exec"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// HandleRestart restarts all production services
func HandleRestart() error {
	return HandleRestartWithFlags(false)
}

// HandleRestartForce restarts all production services, bypassing quorum checks
func HandleRestartForce() error {
	return HandleRestartWithFlags(true)
}

// HandleRestartWithFlags restarts all production services with optional force flag
func HandleRestartWithFlags(force bool) error {
	if err := clierr.RequireRoot("restarting the node services"); err != nil {
		return err
	}

	// Pre-flight: check if restarting this node would temporarily break quorum
	if !force {
		if warning := checkQuorumSafety(); warning != "" {
			return clierr.Conflict("%s\n  Use 'orama node restart --force' to proceed anyway.", warning)
		}
	}

	fmt.Printf("Restarting all Orama production services...\n")

	services := utils.GetProductionServices()
	if len(services) == 0 {
		fmt.Printf("  No Orama services found\n")
		return nil
	}

	// The TLS/DNS frontend (caddy, coredns) is NOT part of GetProductionServices,
	// but caddy/coredns declare `Requires=orama-node.service`, so stopping
	// orama-node below cascade-stops them via systemd. `Requires` propagates a
	// STOP but never a START, and StartServicesOrdered only starts the orama
	// services — so without this a bare `orama node restart` leaves caddy dead
	// and the node's :443 HTTPS frontend offline until the next reboot. Capture
	// which frontend units are running now and bring exactly those back at the
	// end, after the gateway is healthy (caddy's own ExecStartPre gates on it).
	frontendToRestore := activeFrontendServices()

	// Stop namespace services first (same as stop command)
	fmt.Printf("\n  Stopping namespace services...\n")
	stopAllNamespaceServices()

	// Ordered stop: node first (supervisor + @index via PartOf), then leftover host units
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
		return clierr.Failure("Error: %v", err)
	}
	if err := utils.EnsurePortsAvailable("prod restart", ports); err != nil {
		return clierr.Failure("Error: %v", err)
	}

	// Start all services in dependency order
	fmt.Printf("\n  Starting services...\n")
	utils.StartServicesOrdered(services, "start")

	// Bring the TLS/DNS frontend back up (see capture above). Done last so the
	// embedded gateway is already started; caddy's ExecStartPre then clears its
	// index gateway /health wait quickly instead of timing out.
	for _, svc := range frontendToRestore {
		if err := exec.Command("systemctl", "start", svc).Run(); err != nil {
			fmt.Printf("  Warning: Failed to start %s: %v\n", svc, err)
		} else {
			fmt.Printf("  Started %s\n", svc)
		}
	}

	fmt.Printf("\n All services restarted\n")
	return nil
}

// frontendServices are the TLS/DNS units that sit in front of the node and are
// torn down (but not brought back) by an orama-node restart — see HandleRestartWithFlags.
var frontendServices = []string{"coredns", "caddy"}

// activeFrontendServices returns the frontend units that are installed AND
// currently active, so a restart can restore exactly the set that was running.
func activeFrontendServices() []string {
	return selectFrontendToRestore(frontendServices, func(svc string) bool {
		if !utils.ServiceUnitExists(svc) {
			return false
		}
		running, _ := utils.IsServiceActive(svc)
		return running
	})
}

// selectFrontendToRestore filters candidates to those shouldRestore reports
// true for, preserving order. Split from the systemd probing so the restore
// policy is unit-testable without a live host.
func selectFrontendToRestore(candidates []string, shouldRestore func(string) bool) []string {
	var out []string
	for _, svc := range candidates {
		if shouldRestore(svc) {
			out = append(out, svc)
		}
	}
	return out
}
