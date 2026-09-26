package upgrade

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/utils"
	"github.com/DeBrosOfficial/network/pkg/constants"
	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/nodehealth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// clusterHealthBudget is how long this node has to rejoin after its own
// restart before the upgrade fails.
const clusterHealthBudget = 5 * time.Minute

// supervisorService is orama-node as utils.GetProductionServices names it.
const supervisorService = "orama-node"

// restartServices brings the node back after the swap.
//
// The leadership hand-over is not here: it ran before the stop
// (preStopSteps), which is the only point at which this node could still lead
// and the only point at which there was an rqlited to read.
func (o *Orchestrator) restartServices() error {
	fmt.Printf("\n🔄 Restarting services with rolling restart...\n")

	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}

	services := utils.GetProductionServices()
	if len(services) == 0 {
		return fmt.Errorf("no services found to restart: %s is missing", supervisorUnit)
	}

	// Unmask and re-enable all services BEFORE restarting them.
	// "orama node stop" masks services (symlinks unit to /dev/null) to prevent
	// Restart=always from reviving them. We must unmask first, then re-enable,
	// so that all services (including namespace services) can actually start.
	for _, svc := range services {
		masked, err := utils.IsServiceMasked(svc)
		if err != nil {
			return fmt.Errorf("check whether %s is masked: %w", svc, err)
		}
		if masked {
			if err := exec.Command("systemctl", "unmask", svc).Run(); err != nil {
				return fmt.Errorf("unmask %s: %w", svc, err)
			}
		}
		if err := exec.Command("systemctl", "enable", svc).Run(); err != nil {
			return fmt.Errorf("enable %s: %w", svc, err)
		}
	}

	// orama-node first: it is the supervisor, and it starts the whole
	// orama-namespace-*@index stack itself. The pre-factory host daemons
	// (installers.LegacyHostUnits) are not restarted: Phase 5 deleted them.
	fmt.Printf("   Starting %s...\n", supervisorService)
	// Fatal. Everything after this point assumes the supervisor came up.
	if err := exec.Command("systemctl", "restart", supervisorService).Run(); err != nil {
		return fmt.Errorf("restart %s: %w", supervisorService, err)
	}
	fmt.Printf("   ✓ Started %s\n", supervisorService)

	// Fatal, not a warning. This gate exists to stop the rollout before the
	// next voter is restarted. The remote rollout stops here too, so the
	// remaining nodes keep serving.
	fmt.Printf("   Waiting for the node to rejoin the cluster...\n")
	if err := o.waitForClusterHealth(clusterHealthBudget); err != nil {
		return fmt.Errorf("this node did not rejoin the cluster after its restart: %w", err)
	}
	fmt.Printf("   ✓ Node is carrying its share again\n")

	// Restart remaining services (namespace + any others) in dependency order.
	// Namespace services are restarted: rqlite → olric (+ wait) → gateway.
	var remaining []string
	for _, svc := range services {
		if svc != supervisorService {
			remaining = append(remaining, svc)
		}
	}
	utils.StartServicesOrdered(remaining, "restart")

	fmt.Printf("   ✓ All services restarted\n")
	return nil
}

// waitForClusterHealth waits for this node to be carrying its share again.
//
// Through nodehealth, which is the same gate install verification and the
// rolling upgrade use. The version this replaces polled the index rqlite
// and accepted any Leader or Follower — so a node that rejoined but was 40,000
// entries behind, or whose gateway never came back, counted as healthy.
func (o *Orchestrator) waitForClusterHealth(timeout time.Duration) error {
	indexRQLite, err := rqlite.EndpointFromNodeConfig(oramainstall.OramaRoot(o.oramaDir), o.nodeConfigPath())
	if err != nil {
		return err
	}
	return nodehealth.WaitReady(context.Background(), nodehealth.Target{
		RQLite:      indexRQLite,
		GatewayBase: fmt.Sprintf("http://localhost:%d", constants.GatewayAPIPort),
	}, nodehealth.Options{
		Budget:             timeout,
		RequireLeaderKnown: true,
	})
}
