package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// renderedIDs is what node.yaml names this node by.
type renderedIDs struct {
	Node struct {
		ID string `yaml:"id"`
	} `yaml:"node"`
	HTTPGateway struct {
		NodeName string `yaml:"node_name"`
	} `yaml:"http_gateway"`
}

func renderNodeIDs(t *testing.T, oramaDir, domain, baseDomain string) renderedIDs {
	t.Helper()
	out, err := NewConfigGenerator(oramaDir).GenerateNodeConfig(nil, "10.0.0.5", "", domain, baseDomain, false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig: %v", err)
	}
	var ids renderedIDs
	if err := yaml.Unmarshal([]byte(out), &ids); err != nil {
		t.Fatalf("parse rendered node.yaml: %v", err)
	}
	return ids
}

// Nameservers are installed with the base domain as their domain. The id used
// to be its first label, so every nameserver of a cluster was "example".
func TestGenerateNodeConfig_nameserversGetDistinctIDs(t *testing.T) {
	a := renderNodeIDs(t, t.TempDir(), "example.test", "example.test")
	b := renderNodeIDs(t, t.TempDir(), "example.test", "example.test")

	if a.Node.ID == "example" || b.Node.ID == "example" {
		t.Fatalf("node.id is still the domain's first label: %q, %q", a.Node.ID, b.Node.ID)
	}
	if a.Node.ID == b.Node.ID {
		t.Fatalf("two nameservers of one cluster share node.id %q", a.Node.ID)
	}
}

// The id is the peer id of the identity the node runs with, and node_name
// follows it.
func TestGenerateNodeConfig_idIsThePeerID(t *testing.T) {
	oramaDir := t.TempDir()
	peerID, err := NewSecretGenerator(oramaDir).EnsureNodeIdentity()
	if err != nil {
		t.Fatal(err)
	}
	ids := renderNodeIDs(t, oramaDir, "node-1.example.test", "example.test")
	if ids.Node.ID != peerID.String() {
		t.Errorf("node.id = %q, want the peer id %s", ids.Node.ID, peerID)
	}
	if ids.HTTPGateway.NodeName != peerID.String() {
		t.Errorf("http_gateway.node_name = %q, want the peer id %s", ids.HTTPGateway.NodeName, peerID)
	}
}

// An upgrade regenerates node.yaml from the identity already on disk: the id
// must be stable across regenerations, never a freshly minted one.
func TestGenerateNodeConfig_idIsStableAcrossRegeneration(t *testing.T) {
	oramaDir := t.TempDir()
	first := renderNodeIDs(t, oramaDir, "node-1.example.test", "example.test")
	second := renderNodeIDs(t, oramaDir, "node-1.example.test", "example.test")
	if first.Node.ID != second.Node.ID {
		t.Fatalf("node.id changed between two regenerations: %q -> %q", first.Node.ID, second.Node.ID)
	}
}

// A corrupt identity used to be replaced silently with a new one, giving the
// node a different peer id from every record the cluster holds about it.
func TestEnsureNodeIdentity_refusesACorruptIdentity(t *testing.T) {
	oramaDir := t.TempDir()
	keyPath := filepath.Join(oramaDir, "data", "identity.key")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("not a libp2p key")
	if err := os.WriteFile(keyPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewSecretGenerator(oramaDir).EnsureNodeIdentity(); err == nil || !strings.Contains(err.Error(), keyPath) {
		t.Fatalf("EnsureNodeIdentity on a corrupt key = %v, want an error naming %s", err, keyPath)
	}
	if got, _ := os.ReadFile(keyPath); string(got) != string(corrupt) {
		t.Fatal("the corrupt identity was overwritten")
	}
	if _, err := NewConfigGenerator(oramaDir).GenerateNodeConfig(nil, "10.0.0.5", "", "n.example.test", "example.test", false); err == nil {
		t.Fatal("node.yaml was rendered without a usable identity")
	}
}
