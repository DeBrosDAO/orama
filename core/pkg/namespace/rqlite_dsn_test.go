package namespace

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

func TestTenantRQLiteEndpoint_bindAddressAndClusterCredentials(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop(), systemdSpawner: credentialedSpawner(t)}
	ep, err := cm.tenantRQLiteEndpoint("10.0.0.5", 10200)
	if err != nil {
		t.Fatal(err)
	}
	if ep.HostPort() != "10.0.0.5:10200" || ep.Username != testRQLiteUser || ep.Password != testRQLitePass {
		t.Fatalf("got %+v", ep)
	}
}

// The old helper turned an empty or wildcard host into 127.0.0.1, where a
// namespace rqlited never listens.
func TestTenantRQLiteEndpoint_noHostIsAnError(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop(), systemdSpawner: credentialedSpawner(t)}
	for _, host := range []string{"", "0.0.0.0", "::"} {
		if ep, err := cm.tenantRQLiteEndpoint(host, 10200); err == nil {
			t.Errorf("host %q resolved to %s", host, ep)
		}
	}
}

func TestTenantRQLiteEndpoint_missingPasswordIsAnError(t *testing.T) {
	_, namespaceBase := setupOramaDirs(t)
	cm := &ClusterManager{logger: zap.NewNop(), systemdSpawner: NewSystemdSpawner(namespaceBase, "", zap.NewNop())}
	_, err := cm.tenantRQLiteEndpoint("10.0.0.5", 10200)
	if err == nil || !strings.Contains(err.Error(), "rqlite-password") {
		t.Fatalf("want an error naming the password file, got %v", err)
	}
}

func TestWithRQLiteCredentials(t *testing.T) {
	got, err := withRQLiteCredentials("http://10.0.0.5:10200", "orama", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://orama:s3cret@10.0.0.5:10200" {
		t.Fatalf("got %q", got)
	}
	// An already-credentialed DSN (the index gateway's, forwarded as the
	// global DSN) gets the host's credentials too: the rqlite password is
	// cluster-wide, and the embedded one is only ever an older copy of it.
	got, err = withRQLiteCredentials("http://orama:other@10.0.0.1:10100", "orama", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://orama:s3cret@10.0.0.1:10100" {
		t.Fatalf("got %q", got)
	}
	if _, err := withRQLiteCredentials("", "orama", "s3cret"); err == nil {
		t.Fatal("empty DSN accepted")
	}
}

// A gateway YAML read back from disk carries the credentials it was written
// with; after a password change the host's must win, or the gateway keeps
// authenticating with the old one.
func TestWithRQLiteCredentials_hostCredentialsWinOverStaleOnes(t *testing.T) {
	got, err := withRQLiteCredentials("http://orama:old-secret@10.0.0.1:10100", "orama", "new-secret")
	if err != nil {
		t.Fatalf("withRQLiteCredentials: %v", err)
	}
	if strings.Contains(got, "old-secret") || !strings.Contains(got, "new-secret") {
		t.Errorf("stale credentials survived: %s", rqlite.RedactDSN(got))
	}
	if strings.Count(got, "@") != 1 {
		t.Errorf("credentials doubled: %s", rqlite.RedactDSN(got))
	}
}
