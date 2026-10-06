package namespace

import (
	"context"
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
