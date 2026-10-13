package installers

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// The finding: the REST API was guarded by loopback alone, and every process on
// the node is on loopback — a tenant's deployment could unpin any namespace's
// content. Install now makes it require the credentials every consumer derives
// from the cluster secret.
func TestUpdateConfig_requiresCredentialsOnTheClusterAPIs(t *testing.T) {
	dir := t.TempDir()
	writeServiceJSON(t, dir, `{
  "cluster": {},
  "api": {
    "restapi": {"http_listen_multiaddress": "/ip4/0.0.0.0/tcp/10108"},
    "pinsvcapi": {"http_listen_multiaddress": "/ip4/127.0.0.1/tcp/10113"}
  }
}`)
	const secret = "the-cluster-secret"
	ici := NewIPFSClusterInstaller("amd64", io.Discard)
	if err := ici.updateConfig(rootfs.At(filepath.Dir(dir)), dir, secret+"\n", 10107, testSwarmListen, nil); err != nil {
		t.Fatal(err)
	}
	want, err := ipfs.ClusterRESTPassword(secret)
	if err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, filepath.Join(dir, "service.json"))
	if addr := field(got, "ipfs_connector", "ipfshttp", "node_multiaddress"); addr != ipfs.KuboProxyMultiaddr() {
		t.Errorf("node_multiaddress = %v, want the proxy %s", addr, ipfs.KuboProxyMultiaddr())
	}
	for _, section := range []string{"restapi", "pinsvcapi"} {
		creds, _ := field(got, "api", section, "basic_auth_credentials").(map[string]interface{})
		if len(creds) != 1 || creds[ipfs.ClusterRESTUser] != want {
			t.Errorf("api.%s.basic_auth_credentials = %v, want only %s with the derived password", section, creds, ipfs.ClusterRESTUser)
		}
	}
}

// service.json holds the cluster secret and now the REST password; it was
// written world-readable.
func TestUpdateConfig_serviceJSONIsPrivate(t *testing.T) {
	dir := t.TempDir()
	writeServiceJSON(t, dir, `{"cluster": {}}`)
	path := filepath.Join(dir, "service.json")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	ici := NewIPFSClusterInstaller("amd64", io.Discard)
	if err := ici.updateConfig(rootfs.At(filepath.Dir(dir)), dir, "secret", 10107, testSwarmListen, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != serviceJSONMode {
		t.Errorf("service.json mode %v, want %v", info.Mode().Perm(), os.FileMode(serviceJSONMode))
	}
}

// ipfs-cluster's default mDNS (10s) answered multicast DNS on 0.0.0.0:5353 and
// [::]:5353 on every node; peers are configured by overlay address, so install
// turns it off, whether service.json already has a cluster section or not.
func TestUpdateConfig_mdnsIsOff(t *testing.T) {
	for name, initial := range map[string]string{
		"existing section with the default": `{"cluster": {"mdns_interval": "10s"}}`,
		"no cluster section":                `{}`,
	} {
		dir := t.TempDir()
		writeServiceJSON(t, dir, initial)
		ici := NewIPFSClusterInstaller("amd64", io.Discard)
		if err := ici.updateConfig(rootfs.At(filepath.Dir(dir)), dir, "secret", 10107, testSwarmListen, nil); err != nil {
			t.Fatal(err)
		}
		if got := field(readJSON(t, filepath.Join(dir, "service.json")), "cluster", "mdns_interval"); got != "0s" {
			t.Errorf("%s: cluster.mdns_interval = %v, want 0s", name, got)
		}
	}
}
