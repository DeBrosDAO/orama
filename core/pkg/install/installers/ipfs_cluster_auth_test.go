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
