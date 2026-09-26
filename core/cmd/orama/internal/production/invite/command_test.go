package invite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeNodeYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadNodeIdentity_readsDomainAndPublicIP(t *testing.T) {
	path := writeNodeYAML(t, "node:\n  domain: node1.example.com\n  public_ip: 203.0.113.7\n")
	domain, ip, err := readNodeIdentity(path)
	if err != nil || domain != "node1.example.com" || ip != "203.0.113.7" {
		t.Fatalf("got %q, %q, %v", domain, ip, err)
	}
}

// Upgrade records node.public_ip now, so that is what the error sends the
// operator to — with the flag to use when the node cannot detect it.
func TestReadNodeIdentity_missingPublicIPPointsAtUpgrade(t *testing.T) {
	for _, body := range []string{
		"node:\n  domain: node1.example.com\n",
		"node:\n  domain: node1.example.com\n  public_ip: not-an-ip\n",
		"node:\n  domain: node1.example.com\n  public_ip: 10.0.0.2\n",
	} {
		_, _, err := readNodeIdentity(writeNodeYAML(t, body))
		if err == nil {
			t.Fatalf("%q: expected an error", body)
		}
		if !strings.Contains(err.Error(), "orama node upgrade") || !strings.Contains(err.Error(), "--public-ip") {
			t.Errorf("the error must say how to record the address: %v", err)
		}
	}
}

func TestReadNodeIdentity_missingFileIsAnError(t *testing.T) {
	if _, _, err := readNodeIdentity(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("a missing node.yaml must be an error")
	}
}
