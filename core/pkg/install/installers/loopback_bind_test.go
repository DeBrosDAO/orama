package installers

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

func readJSON(t *testing.T, path string) map[string]interface{} {
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

func field(m map[string]interface{}, path ...string) interface{} {
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

// A repo whose API and gateway were rebound to every interface — what a
// 0.122.x node's orama-node did on each start — is bound back to loopback by
// the next install or upgrade.
func TestConfigureAddresses_rebindsAnExposedRepo(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"Addresses":{"API":["/ip4/0.0.0.0/tcp/10107"],"Gateway":["/ip4/0.0.0.0/tcp/8080"],"Announce":["keep"]}}`
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	ii := NewIPFSInstaller("amd64", io.Discard)
	if err := ii.configureAddresses(rootfs.At(filepath.Dir(dir)), dir, 10107, 8080, 4101, "10.0.0.5", "abc123"); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, filepath.Join(dir, "config"))
	for key, want := range map[string]string{
		"API":     "/ip4/127.0.0.1/tcp/10107",
		"Gateway": "/ip4/127.0.0.1/tcp/8080",
		"Swarm":   "/ip4/10.0.0.5/tcp/4101",
	} {
		list, _ := field(got, "Addresses", key).([]interface{})
		if len(list) != 1 || list[0] != want {
			t.Errorf("Addresses.%s = %v, want [%s]", key, list, want)
		}
	}
	if a, _ := field(got, "Addresses", "Announce").([]interface{}); len(a) != 1 {
		t.Errorf("Addresses.Announce not preserved: %v", a)
	}
	auth, _ := field(got, "API", "Authorizations", "orama", "AuthSecret").(string)
	if auth != "bearer:abc123" {
		t.Errorf("API.Authorizations.orama.AuthSecret = %q, want the bearer", auth)
	}
}

// testSwarmListen is the cluster swarm listener of a node at 10.0.0.5.
const testSwarmListen = "/ip4/10.0.0.5/tcp/10114"

func writeServiceJSON(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "service.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The cluster REST API is unauthenticated control of the pinset until
// requireClusterAPIAuth runs. A node whose service.json binds it and the
// pinning API to every interface is rebound. The IPFS proxy is removed:
// it authenticates nobody, and rebinding it to loopback left every process
// on the node able to pin and unpin the cluster.
func TestUpdateConfig_bindsTheClusterAPIsToLoopback(t *testing.T) {
	dir := t.TempDir()
	writeServiceJSON(t, dir, `{
  "cluster": {"secret": "old", "listen_multiaddress": ["/ip4/0.0.0.0/tcp/9100"]},
  "api": {
    "restapi": {"http_listen_multiaddress": "/ip4/0.0.0.0/tcp/10108"},
    "ipfsproxy": {"listen_multiaddress": "/ip4/0.0.0.0/tcp/10111", "node_multiaddress": "/ip4/127.0.0.1/tcp/5001"},
    "pinsvcapi": {"http_listen_multiaddress": "/ip4/0.0.0.0/tcp/10113"}
  }
}`)
	ici := NewIPFSClusterInstaller("amd64", io.Discard)
	if err := ici.updateConfig(rootfs.At(filepath.Dir(dir)), dir, "secret", 10107, testSwarmListen, nil); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, filepath.Join(dir, "service.json"))
	for _, c := range []struct {
		path []string
		want string
	}{
		{[]string{"api", "restapi", "http_listen_multiaddress"}, "/ip4/127.0.0.1/tcp/10108"},
		{[]string{"api", "pinsvcapi", "http_listen_multiaddress"}, "/ip4/127.0.0.1/tcp/10113"},
	} {
		if v := field(got, c.path...); v != c.want {
			t.Errorf("%v = %v, want %s", c.path, v, c.want)
		}
	}
	if _, ok := got["api"].(map[string]interface{})["ipfsproxy"]; ok {
		t.Fatal("service.json still has api.ipfsproxy")
	}
}

// A fresh service.json with no api section still gets the REST API on the
// port its consumers use, on loopback.
func TestUpdateConfig_addsTheRESTAPIWhenMissing(t *testing.T) {
	dir := t.TempDir()
	writeServiceJSON(t, dir, `{"cluster": {}}`)
	ici := NewIPFSClusterInstaller("amd64", io.Discard)
	if err := ici.updateConfig(rootfs.At(filepath.Dir(dir)), dir, "secret", 10107, testSwarmListen, nil); err != nil {
		t.Fatal(err)
	}
	if v := field(readJSON(t, filepath.Join(dir, "service.json")), "api", "restapi", "http_listen_multiaddress"); v != "/ip4/127.0.0.1/tcp/10108" {
		t.Errorf("restapi = %v", v)
	}
}

// An address the installer cannot read is an error, not left exposed.
// The proxy section is deleted rather than parsed, so this is the pinning API.
func TestUpdateConfig_refusesAnUnreadableListenAddress(t *testing.T) {
	dir := t.TempDir()
	writeServiceJSON(t, dir, `{"cluster": {}, "api": {"pinsvcapi": {"http_listen_multiaddress": "/dns4/example.com/tcp/10111"}}}`)
	ici := NewIPFSClusterInstaller("amd64", io.Discard)
	if err := ici.updateConfig(rootfs.At(filepath.Dir(dir)), dir, "secret", 10107, testSwarmListen, nil); err == nil {
		t.Fatal("an unreadable pinning-service address was accepted")
	}
}

func TestLoopbackListenAddr(t *testing.T) {
	for in, want := range map[string]string{
		"/ip4/0.0.0.0/tcp/9095":   "/ip4/127.0.0.1/tcp/9095",
		"/ip4/10.0.0.3/tcp/10111": "/ip4/127.0.0.1/tcp/10111",
		"/ip6/::/tcp/10111":       "/ip4/127.0.0.1/tcp/10111",
		"/ip4/127.0.0.1/tcp/1":    "/ip4/127.0.0.1/tcp/1",
	} {
		got, err := loopbackListenAddr(in)
		if err != nil || got != want {
			t.Errorf("loopbackListenAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/ip4/0.0.0.0/udp/1", "/ip4/0.0.0.0/tcp/0", "/ip4/0.0.0.0/tcp/x", "/ip4/0.0.0.0/tcp/1/p2p/Qm"} {
		if _, err := loopbackListenAddr(bad); err == nil {
			t.Errorf("loopbackListenAddr(%q) accepted", bad)
		}
	}
}

// An upgrade reconciles the swarm listener of an existing node: install used
// to write 0.0.0.0:9100 and orama-node 0.0.0.0:10114; both become the one
// address install owns.
func TestUpdateConfig_reconcilesTheSwarmListener(t *testing.T) {
	for _, old := range []string{"/ip4/0.0.0.0/tcp/9100", "/ip4/0.0.0.0/tcp/10114"} {
		dir := t.TempDir()
		writeServiceJSON(t, dir, `{"cluster": {"secret": "s", "listen_multiaddress": ["`+old+`", "/ip4/0.0.0.0/udp/9096/quic"]}}`)
		ici := NewIPFSClusterInstaller("amd64", io.Discard)
		if err := ici.updateConfig(rootfs.At(filepath.Dir(dir)), dir, "s", 10107, testSwarmListen, nil); err != nil {
			t.Fatal(err)
		}
		got, _ := field(readJSON(t, filepath.Join(dir, "service.json")), "cluster", "listen_multiaddress").([]interface{})
		if len(got) != 1 || got[0] != testSwarmListen {
			t.Errorf("from %s: listen_multiaddress = %v, want [%s]", old, got, testSwarmListen)
		}
	}
}

func TestClusterSwarmListenAddr(t *testing.T) {
	got, err := ClusterSwarmListenAddr("10.0.0.5")
	if err != nil || got != "/ip4/10.0.0.5/tcp/"+strconv.Itoa(constants.IPFSClusterSwarmPort) {
		t.Fatalf("ClusterSwarmListenAddr = %q, %v", got, err)
	}
	// The swarm never binds a public or wildcard address, nor nothing.
	for _, bad := range []string{"", "0.0.0.0", "203.0.113.7", "10.0.1.5", "127.0.0.1", "fd00::5", "not-an-ip"} {
		if _, err := ClusterSwarmListenAddr(bad); err == nil {
			t.Errorf("ClusterSwarmListenAddr(%q) accepted an address outside the mesh", bad)
		}
	}
}

// InitializeConfig refuses before it writes anything when it has no mesh
// address to bind.
func TestInitializeConfig_refusesWithoutAMeshAddress(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "ipfs-cluster")
	ici := NewIPFSClusterInstaller("amd64", io.Discard)
	if err := ici.InitializeConfig(rootfs.At(dir), clusterPath, "secret", 10107, "", nil); err == nil {
		t.Fatal("InitializeConfig accepted an empty swarm address")
	}
	if _, err := os.Stat(clusterPath); !os.IsNotExist(err) {
		t.Error("InitializeConfig created the cluster directory before refusing")
	}
}
