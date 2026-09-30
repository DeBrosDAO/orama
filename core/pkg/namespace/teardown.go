package namespace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// teardownAction is the spawn action that removes a namespace from a node: its
// units stopped AND disabled, its data directory and unit env files deleted.
//
// It is distinct from the stop-* actions, which only stop a unit and leave it
// enabled — right for a restart, wrong for a namespace that is going away: the
// next `orama node upgrade` enables and restarts every namespace unit it finds
// on disk, so a namespace that was rolled back or deleted with only its units
// stopped came back to life on the next upgrade.
const teardownAction = "teardown-namespace"

// platformNamespaces are the node's own instances, never a tenant's: they are
// never torn down as a tenant, whatever the registry says.
var platformNamespaces = map[string]bool{
	"index": true, "nameserver": true, "system": true, "default": true,
}

func isPlatformNamespace(namespace string) bool {
	return namespace == "" || platformNamespaces[strings.TrimSpace(namespace)] || IsReservedNamespace(namespace)
}

// TeardownNamespace removes a tenant namespace from this node: every unit is
// stopped and disabled, then the namespace's data directory and unit env files
// are deleted.
//
// The order is the safety. The data directory and env files are what a restart
// discovers the namespace from, so they are the retry handle: if a unit could
// not be stopped or disabled they are kept, and the failure is returned, so the
// caller (or the tenant reconciler's orphan sweep) tries again instead of
// deleting the files out from under a running process.
func (s *SystemdSpawner) TeardownNamespace(ctx context.Context, namespace string) error {
	if isPlatformNamespace(namespace) {
		return fmt.Errorf("refusing to tear down %q: it is not a tenant namespace", namespace)
	}
	s.logger.Info("Tearing down namespace on this node", zap.String("namespace", namespace))

	teardownUnits := s.teardownUnitsFn
	if teardownUnits == nil {
		teardownUnits = s.systemdMgr.TeardownAllNamespaceServices
	}
	if err := teardownUnits(namespace); err != nil {
		return fmt.Errorf("tear down the units of namespace %s, so its data is kept: %w", namespace, err)
	}

	deleteState := s.deleteStateFn
	if deleteState == nil {
		deleteState = s.DeleteClusterState
	}
	if err := deleteState(namespace); err != nil {
		return fmt.Errorf("remove the state of namespace %s: %w", namespace, err)
	}
	return nil
}

// teardownNamespaceOnNode tears a namespace down on one node, locally or
// through the node's spawn endpoint. A failed remote teardown is recorded for
// replay by sendStopRequest.
func (cm *ClusterManager) teardownNamespaceOnNode(ctx context.Context, node staleClusterNode, namespace string) error {
	if node.NodeID == cm.localNodeID {
		return cm.systemdSpawner.TeardownNamespace(ctx, namespace)
	}
	if node.InternalIP == "" {
		return fmt.Errorf("node %s has no overlay address recorded, so it cannot be asked to tear down %s", node.NodeID, namespace)
	}
	return cm.sendStopRequest(ctx, node.InternalIP, teardownAction, namespace, node.NodeID)
}

// teardownNamespaceOnNodes tears a namespace down on every node, attempting all
// of them and joining the failures.
func (cm *ClusterManager) teardownNamespaceOnNodes(ctx context.Context, nodes []staleClusterNode, namespace string) error {
	var errs []error
	for _, node := range nodes {
		if err := cm.teardownNamespaceOnNode(ctx, node, namespace); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", node.NodeID, err))
		}
	}
	return errors.Join(errs...)
}
