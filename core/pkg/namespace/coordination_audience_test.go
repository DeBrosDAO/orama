package namespace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// A spawn request is stamped for the node it is sent to, and a request that
// names no node cannot be stamped at all.
func TestSignCoordination_isForTheTargetNodeOnly(t *testing.T) {
	const secret = "a cluster secret for the namespace tests"
	path := filepath.Join(t.TempDir(), "cluster-secret")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	cm := &ClusterManager{clusterSecretPath: path, logger: zap.NewNop()}
	key, err := auth.CoordinationKey(secret)
	if err != nil {
		t.Fatal(err)
	}

	stamped := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/internal/namespace/spawn", strings.NewReader(`{}`))
		if err := cm.signCoordination(r, "node-2"); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if auth.VerifyCoordinationV2(key, stamped(), time.Now(), "node-3") {
		t.Fatal("a request stamped for node-2 verified at node-3")
	}
	if !auth.VerifyCoordinationV2(key, stamped(), time.Now(), "node-2") {
		t.Fatal("a request stamped for node-2 did not verify at node-2")
	}
}

func TestSendSpawnRequest_refusesARequestThatNamesNoNode(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop()}
	_, err := cm.sendSpawnRequest(context.Background(), "10.0.0.2", map[string]interface{}{
		"action": "save-cluster-state", "namespace": "acme",
	})
	if err == nil || !strings.Contains(err.Error(), "node_id") {
		t.Fatalf("err = %v, want a refusal that names the missing node_id", err)
	}
}

// saveRemoteState sent no node_id, so the spawn handler answered 400 and every
// remote save of the WebRTC cluster state failed with a warning nobody read.
//
// Mutation check: drop node_id from saveRemoteState and this fails.
func TestSaveRemoteState_namesTheTargetNode(t *testing.T) {
	var got map[string]interface{}
	cm := &ClusterManager{
		logger: zap.NewNop(),
		spawnRequestFn: func(_ context.Context, _ string, req map[string]interface{}) (*spawnResponse, error) {
			got = req
			return &spawnResponse{Success: true}, nil
		},
	}
	cm.saveRemoteState(context.Background(), "10.0.0.2", "node-2", "acme", &ClusterLocalState{})
	if got == nil || got["node_id"] != "node-2" || got["action"] != "save-cluster-state" || got["namespace"] != "acme" {
		t.Fatalf("request = %v, want save-cluster-state for acme addressed to node-2", got)
	}
}
