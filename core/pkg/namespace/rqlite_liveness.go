package namespace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// A namespace whose rqlite does not run is not 'ready'.
//
// 'ready' was written once, when provisioning finished, and by the repair
// paths; nothing demoted it for a member whose rqlited could not start. A
// stagenet namespace sat 'ready' for an hour while one member's unit
// crash-looped 640 times, and every reader of the registry (the status route,
// the e2e suite, the operator) was told it was serving.
//
// Each node reports on its own rqlite unit: after rqliteDownSweepsBeforeDegraded
// consecutive tenant sweeps with the unit not active the cluster is 'degraded'
// and says which node it is, and when that node's unit is active again the
// cluster is settled from its members. The streak damps a unit that is merely
// restarting. Only the cluster status changes: the member's row keeps 'running',
// because a 'failed' row sends the repair path off to replace a node that is
// alive, and a 'degraded' cluster is still served (bugboard #278).

// rqliteDownSweepsBeforeDegraded is how many consecutive tenant sweeps
// (tenantReconcileInterval apart) a namespace's rqlite unit must be found not
// active before the cluster is marked degraded. Three sweeps outlast a normal
// restart and the first systemd restart backoffs.
const rqliteDownSweepsBeforeDegraded = 3

// rqliteDownPrefix starts the cluster's error message while rqlite is down on
// one or more nodes; the node ids follow, sorted and comma separated.
const rqliteDownPrefix = "rqlite is not running on node "

// rqliteDownMessage is the cluster's error message while the given nodes'
// rqlite is down. Every node edits only its own entry, and re-asserts it every
// sweep, so the message converges on the set of nodes that report down even
// though each write replaces the whole message.
func rqliteDownMessage(nodeIDs ...string) string {
	sorted := slices.Clone(nodeIDs)
	slices.Sort(sorted)
	return rqliteDownPrefix + strings.Join(sorted, ", ")
}

// rqliteDownNodes parses the nodes a liveness message names; liveness is false
// when the message is not one (the cluster is degraded for another reason).
func rqliteDownNodes(errorMessage string) (nodes []string, liveness bool) {
	rest, ok := strings.CutPrefix(errorMessage, rqliteDownPrefix)
	if !ok {
		return nil, false
	}
	for _, id := range strings.Split(rest, ", ") {
		if id != "" {
			nodes = append(nodes, id)
		}
	}
	return nodes, true
}

type rqliteLivenessAction int

const (
	rqliteLivenessNone rqliteLivenessAction = iota
	rqliteLivenessDegrade
	rqliteLivenessSettle
)

// decideRQLiteLiveness maps this node's streak of sweeps with the unit down
// (0 when it is running) and the cluster's recorded state to what to write, and
// for a degrade the message to write. members, when not nil, are the cluster's
// current members: a node named in the message that is no longer one is
// dropped, because a node that left can never clear its own entry.
func decideRQLiteLiveness(downSweeps int, status ClusterStatus, errorMessage, localNodeID string, members []string) (rqliteLivenessAction, string) {
	reported, liveness := rqliteDownNodes(errorMessage)
	if members != nil {
		reported = slices.DeleteFunc(reported, func(id string) bool { return !slices.Contains(members, id) })
	}
	mine := slices.Contains(reported, localNodeID)
	degraded := status == ClusterStatusDegraded

	switch {
	case downSweeps >= rqliteDownSweepsBeforeDegraded && status == ClusterStatusReady:
		return rqliteLivenessDegrade, rqliteDownMessage(append(reported, localNodeID)...)
	case downSweeps >= rqliteDownSweepsBeforeDegraded && degraded && !liveness:
		return rqliteLivenessDegrade, rqliteDownMessage(localNodeID)
	case downSweeps >= rqliteDownSweepsBeforeDegraded && degraded && !mine:
		return rqliteLivenessDegrade, rqliteDownMessage(append(reported, localNodeID)...)
	case downSweeps == 0 && degraded && liveness && mine:
		remaining := slices.DeleteFunc(slices.Clone(reported), func(id string) bool { return id == localNodeID })
		if len(remaining) == 0 {
			return rqliteLivenessSettle, ""
		}
		return rqliteLivenessDegrade, rqliteDownMessage(remaining...)
	case downSweeps == 0 && degraded && liveness && len(reported) == 0:
		return rqliteLivenessSettle, ""
	}
	return rqliteLivenessNone, ""
}

// observeRQLite records one sweep's observation for a cluster and returns the
// consecutive sweeps its unit has been down.
func (cm *ClusterManager) observeRQLite(clusterID string, running bool) int {
	cm.rqliteDownMu.Lock()
	defer cm.rqliteDownMu.Unlock()
	if running {
		delete(cm.rqliteDownSweeps, clusterID)
		return 0
	}
	if cm.rqliteDownSweeps == nil {
		cm.rqliteDownSweeps = make(map[string]int)
	}
	cm.rqliteDownSweeps[clusterID]++
	return cm.rqliteDownSweeps[clusterID]
}

// forgetUnassignedObservations drops the down-sweep streaks of clusters this
// node no longer hosts, which would otherwise stay in the map for ever.
func (cm *ClusterManager) forgetUnassignedObservations(assignments []tenantAssignment) {
	assigned := make(map[string]bool, len(assignments))
	for _, a := range assignments {
		assigned[a.ClusterID] = true
	}
	cm.rqliteDownMu.Lock()
	defer cm.rqliteDownMu.Unlock()
	for id := range cm.rqliteDownSweeps {
		if !assigned[id] {
			delete(cm.rqliteDownSweeps, id)
		}
	}
}

// reconcileRQLiteLiveness is the status leg of the per-node sweep. running
// reports whether a namespace's rqlite unit is active and whether that is
// known; an unknown answer leaves the streak and the cluster alone. A failure
// on one namespace does not stop the others; the failures are returned joined,
// each naming its namespace, for the sweep to log.
func (cm *ClusterManager) reconcileRQLiteLiveness(ctx context.Context, running func(namespace string) (active, known bool)) error {
	assignments, err := cm.localAssignments(ctx)
	if err != nil {
		return err
	}
	cm.forgetUnassignedObservations(assignments)

	var errs []error
	for _, a := range assignments {
		active, known := running(a.NamespaceName)
		if !known {
			continue
		}
		down := cm.observeRQLite(a.ClusterID, active)
		if err := cm.reconcileClusterRQLiteLiveness(ctx, a, down); err != nil {
			errs = append(errs, fmt.Errorf("namespace %s: %w", a.NamespaceName, err))
		}
	}
	return errors.Join(errs...)
}

// reconcileClusterRQLiteLiveness applies decideRQLiteLiveness to one cluster.
func (cm *ClusterManager) reconcileClusterRQLiteLiveness(ctx context.Context, a tenantAssignment, down int) error {
	cluster, err := cm.GetCluster(ctx, a.ClusterID)
	if err != nil {
		return fmt.Errorf("read cluster: %w", err)
	}
	if cluster == nil {
		return fmt.Errorf("read cluster %s: it is not in the registry", a.ClusterID)
	}
	var members []string
	if _, liveness := rqliteDownNodes(cluster.ErrorMessage); liveness {
		nodes, err := cm.getClusterNodes(ctx, a.ClusterID)
		if err != nil {
			return fmt.Errorf("read members: %w", err)
		}
		members = make([]string, 0, len(nodes))
		for _, n := range nodes {
			members = append(members, n.NodeID)
		}
	}
	action, msg := decideRQLiteLiveness(down, cluster.Status, cluster.ErrorMessage, cm.localNodeID, members)
	switch action {
	case rqliteLivenessDegrade:
		cm.logger.Error("Namespace rqlite is not running on a node; marking the cluster degraded",
			zap.String("namespace", a.NamespaceName), zap.Int("sweeps_down", down), zap.String("message", msg),
			zap.String("diagnose", "systemctl status orama-namespace-rqlite@"+a.NamespaceName+"; journalctl -u orama-namespace-rqlite@"+a.NamespaceName))
		if err := cm.updateClusterStatus(ctx, a.ClusterID, ClusterStatusDegraded, msg); err != nil {
			return fmt.Errorf("mark degraded: %w", err)
		}
	case rqliteLivenessSettle:
		cm.logger.Info("Namespace rqlite is running again on every node that reported it down; settling the cluster status",
			zap.String("namespace", a.NamespaceName))
		if err := cm.settleClusterStatus(ctx, cluster); err != nil {
			return fmt.Errorf("settle: %w", err)
		}
	}
	return nil
}

// localRQLiteRunning is reconcileRQLiteLiveness's probe: this node's rqlite unit.
func (cm *ClusterManager) localRQLiteRunning(namespace string) (active, known bool) {
	return serviceRunning(cm, namespace, systemd.ServiceTypeRQLite)
}
