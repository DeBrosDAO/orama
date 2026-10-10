package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

func writeEnv(t *testing.T, dir, ns, body string) {
	t.Helper()
	nsDir := filepath.Join(dir, ns)
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "rqlite.env"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func indexEndpoint(t *testing.T) rqlite.Endpoint {
	t.Helper()
	ep, err := rqlite.NewEndpoint("10.0.0.1:10100", testRQLiteUser, testRQLitePass)
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

// Each instance is addressed where its env file says it binds (the WireGuard
// IP, not localhost), with the cluster credentials.
func TestNamespaceRQLiteEndpoints_usesHTTPAddrAndClusterCredentials(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "index", "HTTP_ADDR=10.0.0.1:10100\nRAFT_ADDR=10.0.0.1:10101\n")
	writeEnv(t, dir, "anchat", "# Auto-generated\nNODE_ID=n1\nHTTP_ADDR=10.0.0.1:10200\n")
	if err := os.MkdirAll(filepath.Join(dir, "olric-only"), 0755); err != nil {
		t.Fatal(err)
	}

	got, failures, err := namespaceRQLiteEndpoints(dir, indexEndpoint(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures %v", failures)
	}
	if len(got) != 2 {
		t.Fatalf("got %d endpoints (%v), want index and anchat", len(got), got)
	}
	ep := got["anchat"]
	if ep.HostPort() != "10.0.0.1:10200" || ep.Username != testRQLiteUser || ep.Password != testRQLitePass {
		t.Fatalf("anchat endpoint %v", ep)
	}
}

// Instances spawned before rqlited was bound to the WireGuard IP still have
// HTTP_ADDR=0.0.0.0:<port> (rqlite.env is only rewritten on respawn). They
// listen everywhere, so the advertise address reaches them. Rejecting the
// wildcard aborted pre-upgrade on every node with a namespace mid-upgrade.
func TestNamespaceRQLiteEndpoints_legacyWildcardBindUsesAdvertiseAddress(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "legacy", "HTTP_ADDR=0.0.0.0:10200\nHTTP_ADV_ADDR=10.0.0.4:10200\n")

	got, failures, err := namespaceRQLiteEndpoints(dir, indexEndpoint(t))
	if err != nil || len(failures) != 0 {
		t.Fatalf("err %v failures %v", err, failures)
	}
	if ep := got["legacy"]; ep.HostPort() != "10.0.0.4:10200" {
		t.Fatalf("legacy endpoint %v, want the advertise address", ep)
	}
}

// A namespace that cannot be addressed is reported on its own and does not
// hide the others (or fail the whole node).
func TestNamespaceRQLiteEndpoints_perNamespaceErrors(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "good", "HTTP_ADDR=10.0.0.1:10200\n")
	writeEnv(t, dir, "no-addr", "NODE_ID=n1\n")
	writeEnv(t, dir, "wildcard-no-adv", "HTTP_ADDR=0.0.0.0:10300\n")
	writeEnv(t, dir, "wildcard-adv", "HTTP_ADDR=0.0.0.0:10400\nHTTP_ADV_ADDR=0.0.0.0:10400\n")

	got, failures, err := namespaceRQLiteEndpoints(dir, indexEndpoint(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["good"]; !ok || len(got) != 1 {
		t.Fatalf("endpoints %v, want only good", got)
	}
	for _, ns := range []string{"no-addr", "wildcard-no-adv", "wildcard-adv"} {
		if failures[ns] == nil {
			t.Errorf("namespace %s: no error reported (failures %v)", ns, failures)
		}
	}
}

func TestNamespaceRQLiteEndpoints_missingDirIsEmpty(t *testing.T) {
	got, failures, err := namespaceRQLiteEndpoints(filepath.Join(t.TempDir(), "absent"), indexEndpoint(t))
	if err != nil || len(got) != 0 || len(failures) != 0 {
		t.Fatalf("got %v, %v, %v", got, failures, err)
	}
}
