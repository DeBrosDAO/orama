package namespace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gatewayspec"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// recoverySpawns are the three spawns node replacement and cluster repair make
// onto a node, which all go through the same admission.
var recoverySpawns = map[string]func(cm *ClusterManager, ctx context.Context, c *NamespaceCluster, n *NodeCapacity) error{
	"rqlite": func(cm *ClusterManager, ctx context.Context, c *NamespaceCluster, n *NodeCapacity) error {
		return cm.spawnRQLiteOnNode(ctx, c, n, rqlite.InstanceConfig{Namespace: c.NamespaceName, NodeID: n.NodeID})
	},
	"olric": func(cm *ClusterManager, ctx context.Context, c *NamespaceCluster, n *NodeCapacity) error {
		return cm.spawnOlricOnNode(ctx, c, n, olric.InstanceConfig{Namespace: c.NamespaceName, NodeID: n.NodeID})
	},
	"gateway": func(cm *ClusterManager, ctx context.Context, c *NamespaceCluster, n *NodeCapacity) error {
		return cm.spawnGatewayOnNode(ctx, c, n, gatewayspec.InstanceConfig{Namespace: c.NamespaceName, NodeID: n.NodeID})
	},
}

func recoveryTarget(cm *ClusterManager) (*NamespaceCluster, *NodeCapacity) {
	return &NamespaceCluster{ID: "c1", NamespaceName: "acme"}, &NodeCapacity{NodeID: cm.localNodeID, InternalIP: "10.0.0.1"}
}

func TestRecoverySpawns_refuseADeletedOrDeprovisioningNamespace(t *testing.T) {
	for name, spawn := range recoverySpawns {
		for _, st := range []ClusterStatus{ClusterStatusDeprovisioning, ""} {
			cm, _ := newAdmissionCM(st)
			c, n := recoveryTarget(cm)
			if err := spawn(cm, context.Background(), c, n); !errors.Is(err, ErrNamespaceBeingDeleted) {
				t.Errorf("%s, status %q: %v; want ErrNamespaceBeingDeleted", name, st, err)
			}
		}
	}
}

func TestRecoverySpawns_refuseAClusterThatIsNoLongerTheNamespaces(t *testing.T) {
	for name, spawn := range recoverySpawns {
		cm, reg := newAdmissionCM(ClusterStatusReady)
		reg.id.Store("c2")
		c, n := recoveryTarget(cm)
		if err := spawn(cm, context.Background(), c, n); !errors.Is(err, ErrClusterMismatch) {
			t.Errorf("%s: %v; want ErrClusterMismatch", name, err)
		}
	}
}

// The repair was decided before the delete's teardown, which holds the lock: it
// waits, then finds the cluster deprovisioning and spawns nothing.
func TestRecoverySpawns_waitForATeardownThenAreRefused(t *testing.T) {
	for name, spawn := range recoverySpawns {
		cm, reg := newAdmissionCM(ClusterStatusDegraded)
		teardown, err := cm.systemdSpawner.LockNamespace(context.Background(), "acme")
		if err != nil {
			t.Fatal(err)
		}
		c, n := recoveryTarget(cm)
		done := make(chan error, 1)
		go func() { done <- spawn(cm, context.Background(), c, n) }()
		select {
		case err := <-done:
			t.Fatalf("%s: ran while the teardown held the namespace: %v", name, err)
		case <-time.After(100 * time.Millisecond):
		}
		reg.status.Store(ClusterStatusDeprovisioning)
		teardown()
		select {
		case err := <-done:
			if !errors.Is(err, ErrNamespaceBeingDeleted) {
				t.Errorf("%s: %v; want ErrNamespaceBeingDeleted", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: never returned after the teardown released the namespace", name)
		}
	}
}

func TestRecoverySpawns_aContextThatEndsWhileWaitingIsRefused(t *testing.T) {
	for name, spawn := range recoverySpawns {
		cm, _ := newAdmissionCM(ClusterStatusReady)
		holder, err := cm.systemdSpawner.LockNamespace(context.Background(), "acme")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		c, n := recoveryTarget(cm)
		if err := spawn(cm, ctx, c, n); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: %v; want context.DeadlineExceeded wrapped", name, err)
		}
		cancel()
		holder()
	}
}

// A remote target is asked by its spawn handler, which admits the spawn on the
// cluster id the request carries.
func TestRecoverySpawns_aRemoteSpawnCarriesTheClusterId(t *testing.T) {
	for name, spawn := range recoverySpawns {
		cm, _ := newAdmissionCM(ClusterStatusReady)
		var sent map[string]interface{}
		cm.spawnRequestFn = func(_ context.Context, _ string, req map[string]interface{}) (*spawnResponse, error) {
			sent = req
			return &spawnResponse{Success: true}, nil
		}
		c, _ := recoveryTarget(cm)
		if err := spawn(cm, context.Background(), c, &NodeCapacity{NodeID: "other", InternalIP: "10.0.0.2"}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if sent["cluster_id"] != "c1" {
			t.Errorf("%s: request carried cluster_id %v, want c1", name, sent["cluster_id"])
		}
	}
}
