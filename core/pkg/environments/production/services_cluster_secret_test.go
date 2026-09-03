package production

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateIPFSClusterService_emptySecret(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	ssg := NewSystemdServiceGenerator("/opt/orama", dir)
	if _, err := ssg.GenerateIPFSClusterService("/usr/bin/ipfs-cluster-service"); err == nil {
		t.Fatal("missing cluster-secret must fail closed")
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets", "cluster-secret"), []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ssg.GenerateIPFSClusterService("/usr/bin/ipfs-cluster-service"); err == nil {
		t.Fatal("empty cluster-secret must fail closed")
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets", "cluster-secret"), []byte("deadbeef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unit, err := ssg.GenerateIPFSClusterService("/usr/bin/ipfs-cluster-service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unit, "Environment=CLUSTER_SECRET=deadbeef") {
		t.Fatalf("unit missing CLUSTER_SECRET: %s", unit)
	}
}
