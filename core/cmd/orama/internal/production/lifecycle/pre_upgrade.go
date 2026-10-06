package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/nodehealth"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/unitenv"
	"go.uber.org/zap"
)

// maintenanceRoot is the rootfs anchor the maintenance flag is written under:
// .orama belongs to the orama user, and this runs as root.
var maintenanceRoot = rootfs.At(config.ProductionBaseDir)

const (
	maintenanceFlagPath = config.ProductionBaseDir + "/.orama/maintenance.flag"

	// indexNamespace is the reserved namespace whose rqlite is the cluster
	// registry. It lives under data/namespaces like a tenant but is not one.
	indexNamespace = "index"
)

// HandlePreUpgrade prepares the node for a safe rolling upgrade, while it
// still runs:
//  1. Checks quorum safety
//  2. Writes the maintenance flag
//  3. Transfers leadership on the index RQLite if leader
//  4. Transfers leadership on each namespace RQLite
//  5. Confirms another node leads the index
//
// The upgrade runs it before it stops anything (it is the hand-over before the
// stop, not a step of the restart). On a node whose index rqlited is not
// running — an upgrade re-run after the first attempt stopped it — there is
// nothing to hand over: the node already counts for nothing in quorum, which
// the quorum check establishes, and steps 3 and 5 are skipped.
func HandlePreUpgrade() error {
	if err := clierr.RequireRoot("the pre-upgrade step"); err != nil {
		return err
	}

	fmt.Printf("Pre-upgrade: preparing node for safe restart...\n")

	// 1. Check quorum safety
	warning, rqliteMayRun := quorumVerdict()
	if warning != "" {
		fmt.Fprintf(os.Stderr, "  UNSAFE: %s\n", warning)
		return clierr.Failure("  Aborting pre-upgrade. Use 'orama node stop --force' to override.")
	}
	fmt.Printf("  Quorum check passed\n")

	// 2. Write maintenance flag. Fatal: it is what keeps this node out of
	// rotation while it is down.
	if err := writeMaintenanceFlag(); err != nil {
		return err
	}
	fmt.Printf("  Maintenance flag written\n")

	if !rqliteMayRun {
		fmt.Printf("  The index rqlite is not running on this node: no leadership to hand over\n")
		return nil
	}
	return handOverLeadership()
}

// writeMaintenanceFlag records when this node went into maintenance.
func writeMaintenanceFlag() error {
	if err := maintenanceRoot.MkdirAll(filepath.Dir(maintenanceFlagPath), 0755); err != nil {
		return fmt.Errorf("create the maintenance flag directory: %w", err)
	}
	if err := maintenanceRoot.WriteFile(maintenanceFlagPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
		return fmt.Errorf("write the maintenance flag: %w", err)
	}
	return nil
}

// handOverLeadership is steps 3–5 of HandlePreUpgrade.
func handOverLeadership() error {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	indexRQLite, err := rqlite.LocalNodeEndpoint()
	if err != nil {
		return clierr.Failure("  Cannot reach the index RQLite: %v", err)
	}
	fmt.Printf("  Checking index RQLite leadership (%s)...\n", indexRQLite)
	if err := rqlite.TransferLeadership(indexRQLite, logger); err != nil {
		fmt.Fprintf(os.Stderr, "  UNSAFE: this node still leads the index RQLite: %v\n", err)
		fmt.Fprintf(os.Stderr, "  Restarting it now forces an election and fails in-flight writes.\n")
		return clierr.Failure("  Aborting pre-upgrade. Check the other voters are reachable, then retry.")
	}
	fmt.Printf("  Index RQLite leadership handled\n")

	// Transfer leadership on each namespace RQLite. The index was handled
	// above; tenantRQLiteEndpoints leaves it out.
	nsEndpoints, nsFailures, err := tenantRQLiteEndpoints(unitenv.Dir, config.ProductionNamespacesDataDir, indexRQLite)
	if err != nil {
		return clierr.Failure("  Cannot list the namespace RQLite instances: %v", err)
	}
	// A tenant namespace that cannot be addressed cannot have its leadership
	// transferred; like a failed transfer, that degrades the namespace, not
	// the node's ability to restart safely.
	for ns, ferr := range nsFailures {
		fmt.Printf("  Warning: namespace '%s' RQLite cannot be addressed, leadership not transferred: %v\n", ns, ferr)
	}
	for ns, ep := range nsEndpoints {
		fmt.Printf("  Checking namespace '%s' RQLite leadership (%s)...\n", ns, ep)
		if err := rqlite.TransferLeadership(ep, logger); err != nil {
			// A tenant namespace losing its leader degrades that namespace, not
			// the node's ability to restart safely, so this stays a warning.
			fmt.Printf("  Warning: namespace '%s' leadership transfer: %v\n", ns, err)
		} else {
			fmt.Printf("  Namespace '%s' RQLite leadership handled\n", ns)
		}
	}

	// Confirm another node is actually leading before we stop.
	//
	// TransferLeadership already waits for this node to stop being the leader.
	// What is left to confirm is that somebody else started: a cluster where
	// this node stepped down and nobody was elected has no quorum, and is the
	// one state in which stopping is worse than doing nothing.
	fmt.Printf("  Confirming another node has taken leadership...\n")
	if err := waitForOtherLeader(indexRQLite, leaderHandoverBudget); err != nil {
		fmt.Fprintf(os.Stderr, "  UNSAFE: %v\n", err)
		fmt.Fprintf(os.Stderr, "  Stopping this node now would remove a voter from a cluster with no leader.\n")
		return clierr.Failure("  Aborting pre-upgrade. Check the other voters are reachable, then retry.")
	}
	fmt.Printf("  Another node is leading; safe to stop\n")
	fmt.Printf("Pre-upgrade complete. Node is ready for the stop.\n")
	return nil
}

// namespaceRQLiteEndpoints finds every rqlite instance in the env tree dir
// (<dir>/<ns>/rqlite.env, "index" included; see tenantRQLiteEndpoints) and returns
// namespace → endpoint. The address comes from the instance's rqlite.env
// (rqlite.InstanceAddrFromEnv, which handles the pre-WireGuard-bind wildcard
// HTTP_ADDR); the credentials are the cluster-wide ones from node.yaml (index),
// which every instance's -auth file carries.
//
// A namespace that cannot be addressed is reported in the second map rather
// than failing the whole node: one tenant's broken env file must not block
// upgrading the node. The error is only for an unreadable dir.
func namespaceRQLiteEndpoints(dir string, index rqlite.Endpoint) (map[string]rqlite.Endpoint, map[string]error, error) {
	endpoints := make(map[string]rqlite.Endpoint)
	failures := make(map[string]error)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return endpoints, failures, nil
		}
		return nil, nil, fmt.Errorf("read %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ns := entry.Name()
		ep, ok, err := rqlite.InstanceEndpointFromEnv(rqlite.InstanceEnvFile(dir, ns), index.Username, index.Password)
		switch {
		case err != nil:
			failures[ns] = err
		case ok:
			endpoints[ns] = ep
		}
	}
	return endpoints, failures, nil
}

// leaderHandoverBudget is how long another voter has to win the election this
// node's step-down triggered.
const leaderHandoverBudget = 60 * time.Second

// waitForOtherLeader blocks until this node reports a leader that is not
// itself.
//
// A node that is a Follower with an empty leader_id is in a cluster that cannot
// commit a write; "Follower" alone is not the safety property, "somebody is
// leading" is.
func waitForOtherLeader(ep rqlite.Endpoint, budget time.Duration) error {
	target := nodehealth.Target{RQLite: ep}
	client := &http.Client{Timeout: 5 * time.Second}

	deadline := time.Now().Add(budget)
	last := "unknown"
	for {
		st, err := nodehealth.Observe(context.Background(), client, target)
		if err == nil {
			last = fmt.Sprintf("state %s, leader %q", st.RaftState, st.LeaderID)
			if !strings.EqualFold(st.RaftState, "Leader") && st.LeaderID != "" {
				return nil
			}
		} else {
			last = err.Error()
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("no other node took leadership within %s (%s)", budget, last)
		}
		time.Sleep(2 * time.Second)
	}
}

// ClearMaintenanceFlag removes the flag HandlePreUpgrade wrote.
//
// It is separate from HandlePostUpgrade because the upgrade orchestrator
// restarts the services itself and only needs this last step; calling the whole
// post-upgrade would start everything a second time.
func ClearMaintenanceFlag() error {
	if err := maintenanceRoot.Remove(maintenanceFlagPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the maintenance flag: %w", err)
	}
	return nil
}
