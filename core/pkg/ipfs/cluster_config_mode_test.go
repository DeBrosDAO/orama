package ipfs

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// service.json holds the cluster secret and the REST password. Install wrote
// it 0600 and every rewrite by the node (peer addresses) left it 0644:
// world-readable on every running node (stagenet e2e storage
// TestCluster_restBasicAuth, 2026-09-30). A rewrite now also tightens a file an
// older node left open.
func TestSaveConfig_keepsServiceJSONPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.json")
	if err := os.WriteFile(path, []byte(`{"cluster":{"secret":"s3cr3t","peer_addresses":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cm := &ClusterConfigManager{clusterPath: dir, logger: zap.NewNop()}
	if err := cm.UpdatePeerAddresses([]string{"/ip4/10.0.0.2/tcp/10114/p2p/12D3KooWExample"}); err != nil {
		t.Fatalf("UpdatePeerAddresses: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != ServiceJSONMode {
		t.Fatalf("service.json mode %v after a rewrite, want %v", info.Mode().Perm(), os.FileMode(ServiceJSONMode))
	}
}
