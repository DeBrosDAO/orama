package namespace

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// The status route reported every cluster's namespace as "" (stagenet e2e,
// 2026-09-30: TestNamespaceCreate_apiProvisionsAndServes), so a client polling
// a cluster id could not tell which namespace it was.
func TestGetClusterStatus_namesTheNamespace(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if strings.Contains(query, "FROM namespace_clusters") {
			appendToSlice(dest, map[string]any{"ID": "cluster-1", "NamespaceName": "acme", "Status": ClusterStatusReady})
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	status, err := cm.GetClusterStatus(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Namespace != "acme" || status.ClusterID != "cluster-1" || status.Status != ClusterStatusReady {
		t.Fatalf("status = %+v", status)
	}
}

// namespace_cluster_nodes holds a row for each node and role, so a cluster of
// three nodes has nine rows. Nodes listed one peer id per row: every node
// three times.
func TestGetClusterStatus_listsEachNodeOnce(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		switch {
		case strings.Contains(query, "FROM namespace_clusters"):
			appendToSlice(dest, map[string]any{"ID": "cluster-1", "NamespaceName": "acme", "Status": ClusterStatusReady})
		case strings.Contains(query, "FROM namespace_cluster_nodes"):
			for _, node := range []string{"node-a", "node-b", "node-c"} {
				for _, role := range []NodeRole{NodeRoleRQLiteLeader, NodeRoleOlric, NodeRoleGateway} {
					appendToSlice(dest, map[string]any{"NodeID": node, "Role": role, "Status": NodeStatusRunning,
						"RQLiteHTTPPort": 10000, "OlricHTTPPort": 10002, "GatewayHTTPPort": 10004})
				}
			}
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	status, err := cm.GetClusterStatus(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"node-a", "node-b", "node-c"}; !slices.Equal(status.Nodes, want) {
		t.Errorf("Nodes = %v, want %v", status.Nodes, want)
	}
	if !status.RQLiteReady || !status.OlricReady || !status.GatewayReady || !status.DNSReady {
		t.Errorf("a cluster whose every row runs is ready: %+v", status)
	}
}

// One row not running keeps the whole cluster from being ready, as it did
// before the nodes were de-duplicated.
func TestGetClusterStatus_oneRoleNotRunningIsNotReady(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		switch {
		case strings.Contains(query, "FROM namespace_clusters"):
			appendToSlice(dest, map[string]any{"ID": "cluster-1", "Status": ClusterStatusProvisioning})
		case strings.Contains(query, "FROM namespace_cluster_nodes"):
			appendToSlice(dest, map[string]any{"NodeID": "node-a", "Status": NodeStatusRunning, "RQLiteHTTPPort": 10000})
			appendToSlice(dest, map[string]any{"NodeID": "node-a", "Status": NodeStatusStarting, "GatewayHTTPPort": 10004})
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	status, err := cm.GetClusterStatus(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Nodes) != 1 {
		t.Errorf("Nodes = %v, want node-a once", status.Nodes)
	}
	if status.RQLiteReady || status.GatewayReady || status.DNSReady {
		t.Errorf("a cluster with a role still starting is not ready: %+v", status)
	}
}

func TestGetClusterStatus_noNodesIsNotReady(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if strings.Contains(query, "FROM namespace_clusters") {
			appendToSlice(dest, map[string]any{"ID": "cluster-1"})
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	status, err := cm.GetClusterStatus(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Nodes) != 0 || status.RQLiteReady || status.DNSReady {
		t.Errorf("status = %+v, want no nodes and nothing ready", status)
	}
}

// A failed read of the node rows was dropped, and the status said a cluster
// had no nodes and nothing ready when its nodes could not be read at all.
func TestGetClusterStatus_nodeReadErrorIsReturned(t *testing.T) {
	errDB := errors.New("rqlite: connection refused")
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if strings.Contains(query, "FROM namespace_clusters") {
			appendToSlice(dest, map[string]any{"ID": "cluster-1"})
			return nil
		}
		return errDB
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	status, err := cm.GetClusterStatus(context.Background(), "cluster-1")
	if !errors.Is(err, errDB) {
		t.Fatalf("err = %v, want it to wrap %v", err, errDB)
	}
	if status != nil {
		t.Errorf("status = %+v, want nil with the error", status)
	}
	if !strings.Contains(err.Error(), "cluster-1") {
		t.Errorf("err = %q, want it to name the cluster", err)
	}
}
