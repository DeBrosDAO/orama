package namespace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/systemd"
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

// teardownSFUAction and teardownTURNAction retire one WebRTC service of a
// namespace that stays (WebRTC was disabled): stopped, disabled, env file
// removed. "stop-sfu"/"stop-turn" keep their restart meaning.
const (
	teardownSFUAction  = "teardown-sfu"
	teardownTURNAction = "teardown-turn"
)

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

// TeardownNamespaceAndData is TeardownNamespace for a namespace that is being
// deleted: once its units are down and its state is gone, the tenant data it
// keeps outside its own directory — its SQLite databases and the directories of
// its deployments — is removed too, so a namespace created again under the same
// name does not inherit it. Nothing is removed if the teardown failed.
func (s *SystemdSpawner) TeardownNamespaceAndData(ctx context.Context, namespace string) error {
	if err := s.TeardownNamespace(ctx, namespace); err != nil {
		return err
	}
	removeData := s.removeTenantDataFn
	if removeData == nil {
		removeData = s.systemdMgr.RemoveTenantData
	}
	if err := removeData(namespace); err != nil {
		return fmt.Errorf("remove the tenant data of namespace %s: %w", namespace, err)
	}
	return nil
}

// teardownNamespaceOnNode tears a namespace down on one node, locally or
// through the node's spawn endpoint. A failed remote teardown is recorded for
// replay by sendStopRequest.
func (cm *ClusterManager) teardownNamespaceOnNode(ctx context.Context, node staleClusterNode, namespace string, scope cleanupScope) error {
	if node.NodeID == cm.localNodeID {
		return cm.teardownLocal(ctx, namespace, scope.PurgeData)
	}
	if node.InternalIP == "" {
		return fmt.Errorf("node %s has no overlay address recorded, so it cannot be asked to tear down %s", node.NodeID, namespace)
	}
	return cm.sendStopRequest(ctx, node.InternalIP, teardownAction, namespace, node.NodeID, scope)
}

// teardownNamespaceOnNodes tears a namespace down on every node, attempting all
// of them and joining the failures.
func (cm *ClusterManager) teardownNamespaceOnNodes(ctx context.Context, nodes []staleClusterNode, namespace string, scope cleanupScope) error {
	var errs []error
	for _, node := range nodes {
		if err := cm.teardownNamespaceOnNode(ctx, node, namespace, scope); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", node.NodeID, err))
		}
	}
	return errors.Join(errs...)
}

// teardownWebRTCService stops and disables one WebRTC unit of a namespace and
// removes its env file, through teardownServiceFn when a test replaces it.
func (s *SystemdSpawner) teardownWebRTCService(namespace string, svc systemd.ServiceType) error {
	if isPlatformNamespace(namespace) {
		return fmt.Errorf("refusing to tear down %s of %q: it is not a tenant namespace", svc, namespace)
	}
	s.logger.Info("Tearing down WebRTC service via systemd",
		zap.String("namespace", namespace), zap.String("service", string(svc)))
	teardown := s.teardownServiceFn
	if teardown == nil {
		teardown = s.systemdMgr.TeardownServiceAndEnv
	}
	if err := teardown(namespace, svc); err != nil {
		return fmt.Errorf("tear down the %s unit of namespace %s: %w", svc, namespace, err)
	}
	return nil
}

// TeardownSFU retires a namespace's SFU on this node: the unit is stopped and
// disabled, its env file and its config (which carries the TURN secret) are
// removed. The upgrade restart discovers units from the env file, so without
// this a namespace that turned WebRTC off got its SFU back on the next upgrade.
func (s *SystemdSpawner) TeardownSFU(ctx context.Context, namespace, nodeID string) error {
	if err := s.teardownWebRTCService(namespace, systemd.ServiceTypeSFU); err != nil {
		return err
	}
	configs, err := filepath.Glob(filepath.Join(s.namespaceBase, namespace, "configs", "sfu-*.yaml"))
	if err != nil {
		return fmt.Errorf("find the SFU configs of namespace %s: %w", namespace, err)
	}
	var errs []error
	for _, path := range configs {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove the SFU config %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

// TeardownTURN retires the namespace's per-namespace TURN unit on this node.
//
// Since bugboard #283 TURN is one shared host unit (orama-turn.service) serving
// every namespace, and no per-namespace unit is created any more; what this
// retires is the pre-#283 orama-namespace-turn@<ns> unit an upgraded node may
// still carry. It NEVER touches the shared unit: dropping the namespace from the
// shared server is ReconcileHostTURN's job, which rewrites the tenant list
// without restarting the process.
func (s *SystemdSpawner) TeardownTURN(ctx context.Context, namespace, nodeID string) error {
	return s.teardownWebRTCService(namespace, systemd.ServiceTypeTURN)
}

// teardownWebRTCOnNode retires one WebRTC service on a node, locally or through
// its spawn endpoint. A failed remote teardown is recorded for replay by
// sendStopRequest — including a node still on the previous release that answers
// "unknown action".
func (cm *ClusterManager) teardownWebRTCOnNode(ctx context.Context, nodeID, nodeIP, namespace, clusterID, action string) error {
	if nodeID == cm.localNodeID {
		if action == teardownTURNAction {
			return cm.systemdSpawner.TeardownTURN(ctx, namespace, nodeID)
		}
		return cm.systemdSpawner.TeardownSFU(ctx, namespace, nodeID)
	}
	if nodeIP == "" {
		return fmt.Errorf("node %s has no overlay address recorded, so it cannot be asked to %s %s", nodeID, action, namespace)
	}
	return cm.sendStopRequest(ctx, nodeIP, action, namespace, nodeID, cleanupScope{ClusterID: clusterID})
}
