package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/encryption"
)

// A standalone gateway never received the node's peer id, so SQLite home-node
// assignment and deployment placement matched no node.
func TestNodePeerID_readsTheNodesIdentity(t *testing.T) {
	dir := t.TempDir()
	id, err := encryption.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := encryption.SaveIdentity(id, filepath.Join(dir, "data", "identity.key")); err != nil {
		t.Fatal(err)
	}
	got, err := nodePeerID(dir)
	if err != nil {
		t.Fatalf("nodePeerID: %v", err)
	}
	if got != id.PeerID.String() {
		t.Errorf("nodePeerID = %q, want %q", got, id.PeerID.String())
	}
}

func TestNodePeerID_missingOrCorruptIdentity(t *testing.T) {
	if _, err := nodePeerID(t.TempDir()); err == nil {
		t.Error("a node with no identity key produced a peer id")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "identity.key"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := nodePeerID(dir); err == nil {
		t.Error("a corrupt identity key produced a peer id")
	}
}

// installedNode lays out an orama directory the way install does: the cluster
// secret under secrets/ and the identity key under data/. It returns the
// cluster secret path and the node's peer id.
func installedNode(t *testing.T, secret string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	id, err := encryption.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := encryption.SaveIdentity(id, filepath.Join(dir, "data", "identity.key")); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(dir, "secrets", "cluster-secret")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretPath, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	return secretPath, id.PeerID.String()
}

func TestLoadNodeIdentity_readsSecretDirectoryAndPeerID(t *testing.T) {
	path, peerID := installedNode(t, "  the-secret\n")

	got, err := loadNodeIdentity(" " + path + " ")
	if err != nil {
		t.Fatalf("loadNodeIdentity: %v", err)
	}
	if got.clusterSecret != "the-secret" {
		t.Errorf("cluster secret = %q", got.clusterSecret)
	}
	if got.oramaDir != filepath.Dir(filepath.Dir(path)) {
		t.Errorf("orama dir = %q", got.oramaDir)
	}
	if got.peerID != peerID {
		t.Errorf("peer id = %q, want %q", got.peerID, peerID)
	}
}

// Without cluster_secret_path the gateway had no NodePeerID, and home-node
// assignment, placement, host TURN and locality silently matched no node.
func TestLoadNodeIdentity_pathIsRequired(t *testing.T) {
	for _, path := range []string{"", "   "} {
		_, err := loadNodeIdentity(path)
		if err == nil || !strings.Contains(err.Error(), "cluster_secret_path is required") {
			t.Errorf("path %q: got %v, want it required", path, err)
		}
	}
}

func TestLoadNodeIdentity_failures(t *testing.T) {
	emptySecret, _ := installedNode(t, " \n")
	noIdentity := filepath.Join(t.TempDir(), "secrets", "cluster-secret")
	if err := os.MkdirAll(filepath.Dir(noIdentity), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(noIdentity, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ path, want string }{
		"unreadable secret": {filepath.Join(t.TempDir(), "missing"), "read cluster secret"},
		"empty secret":      {emptySecret, "is empty"},
		"no identity key":   {noIdentity, "identity"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadNodeIdentity(tc.path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
