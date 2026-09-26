package ipfs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"go.uber.org/zap"
)

// installedServiceJSON is a service.json as install writes it: the swarm on
// the WireGuard address, every API on loopback.
const installedServiceJSON = `{
  "cluster": {
    "secret": "old",
    "listen_multiaddress": ["/ip4/10.0.0.5/tcp/10114"],
    "peer_addresses": [],
    "leave_on_shutdown": false
  },
  "consensus": {"crdt": {"cluster_name": "ipfs-cluster", "trusted_peers": ["*"], "repair_interval": "1h0m0s"}},
  "api": {
    "restapi": {"http_listen_multiaddress": "/ip4/127.0.0.1/tcp/10108"},
    "ipfsproxy": {"listen_multiaddress": "/ip4/127.0.0.1/tcp/9095", "node_multiaddress": "/ip4/127.0.0.1/tcp/10107"},
    "pinsvcapi": {"http_listen_multiaddress": "/ip4/127.0.0.1/tcp/9097"}
  },
  "ipfs_connector": {"ipfshttp": {"node_multiaddress": "/ip4/127.0.0.1/tcp/10107"}}
}`

func clusterManager(t *testing.T, serviceJSON string) (*ClusterConfigManager, string) {
	t.Helper()
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "ipfs-cluster")
	if err := os.MkdirAll(clusterPath, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(clusterPath, "service.json")
	if serviceJSON != "" {
		if err := os.WriteFile(path, []byte(serviceJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{}
	cfg.Database.IPFS.ClusterAPIURL = "http://localhost:10108"
	cfg.Database.IPFS.APIURL = "http://localhost:10107"
	cfg.Node.DataDir = dir
	return &ClusterConfigManager{cfg: cfg, logger: zap.NewNop(), clusterPath: clusterPath, secret: "shared-secret"}, path
}

func readServiceJSON(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func jsonField(m map[string]interface{}, path ...string) interface{} {
	var cur interface{} = m
	for _, p := range path {
		next, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = next[p]
	}
	return cur
}

// Every listener is install's. The node used to rewrite the swarm to
// 0.0.0.0:10114 while install wrote 9100, and the APIs to ports it derived
// from the REST API URL.
func TestEnsureConfig_leavesTheListenersToInstall(t *testing.T) {
	cm, path := clusterManager(t, installedServiceJSON)
	before := readServiceJSON(t, path)

	if err := cm.EnsureConfig(); err != nil {
		t.Fatal(err)
	}
	after := readServiceJSON(t, path)

	for _, p := range [][]string{
		{"cluster", "listen_multiaddress"},
		{"cluster", "leave_on_shutdown"},
		{"consensus", "crdt", "repair_interval"},
		{"api"},
		{"ipfs_connector"},
	} {
		if !reflect.DeepEqual(jsonField(before, p...), jsonField(after, p...)) {
			t.Errorf("%v changed: %v -> %v", p, jsonField(before, p...), jsonField(after, p...))
		}
	}
}

// What the node does own it still writes.
func TestEnsureConfig_writesTheNodesOwnFields(t *testing.T) {
	cm, path := clusterManager(t, installedServiceJSON)
	if err := cm.EnsureConfig(); err != nil {
		t.Fatal(err)
	}
	got := readServiceJSON(t, path)
	if v := jsonField(got, "cluster", "secret"); v != "shared-secret" {
		t.Errorf("secret = %v", v)
	}
	if v := jsonField(got, "consensus", "crdt", "cluster_name"); v != "orama-cluster" {
		t.Errorf("cluster_name = %v", v)
	}
	if v, _ := jsonField(got, "consensus", "crdt", "trusted_peers").([]interface{}); len(v) != 1 || v[0] != "*" {
		t.Errorf("trusted_peers = %v", v)
	}
}

// A node with no service.json was not installed by this release; the file is
// install's to create, not something to invent from a template.
func TestEnsureConfig_missingServiceJSONIsAnError(t *testing.T) {
	cm, path := clusterManager(t, "")
	err := cm.EnsureConfig()
	if err == nil || !strings.Contains(err.Error(), "orama node upgrade") {
		t.Fatalf("EnsureConfig without service.json = %v, want an error pointing at the upgrade", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("EnsureConfig created service.json")
	}
}

func TestUpdatePeerAddresses_keepsInstallsListeners(t *testing.T) {
	cm, path := clusterManager(t, installedServiceJSON)
	peers := []string{"/ip4/10.0.0.2/tcp/10114/p2p/12D3KooWA", "/ip4/10.0.0.2/tcp/10114/p2p/12D3KooWA"}
	if err := cm.UpdatePeerAddresses(peers); err != nil {
		t.Fatal(err)
	}
	got := readServiceJSON(t, path)
	if v, _ := jsonField(got, "cluster", "peer_addresses").([]interface{}); len(v) != 1 {
		t.Errorf("peer_addresses = %v, want the one unique peer", v)
	}
	if v, _ := jsonField(got, "cluster", "listen_multiaddress").([]interface{}); len(v) != 1 || v[0] != "/ip4/10.0.0.5/tcp/10114" {
		t.Errorf("listen_multiaddress = %v, want install's", v)
	}
}

// Discovery keeps the addresses on the swarm port install binds, and nothing
// else. It used to keep /tcp/9100 only, which no node listened on.
func TestClusterSwarmAddrs(t *testing.T) {
	got := clusterSwarmAddrs([]string{
		"/ip4/10.0.0.2/tcp/10114/p2p/12D3KooWA",
		"/ip4/10.0.0.2/tcp/9100/p2p/12D3KooWA",
		"/ip4/127.0.0.1/tcp/10108",
		"/ip4/10.0.0.2/tcp/101140/p2p/12D3KooWA",
	})
	if len(got) != 1 || got[0] != "/ip4/10.0.0.2/tcp/10114/p2p/12D3KooWA" {
		t.Errorf("clusterSwarmAddrs = %v, want only the 10114 swarm address", got)
	}
}
