package cli

import (
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func newTestPeerID(t *testing.T) string {
	t.Helper()
	_, pub, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func TestPeerIDFromNodeConfig_readsNodeID(t *testing.T) {
	want := newTestPeerID(t)
	got, err := peerIDFromNodeConfig([]byte("node:\n  id: \""+want+"\"\n  data_dir: \"/x\"\n"), "node.yaml")
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}

func TestPeerIDFromNodeConfig_refusesWhatIsNotAPeerID(t *testing.T) {
	_, err := peerIDFromNodeConfig([]byte("node:\n  id: \"node-1\"\n"), "node.yaml")
	if err == nil || !strings.Contains(err.Error(), "orama node upgrade") {
		t.Fatalf("a domain label was accepted as a peer id, or the way out is not named: %v", err)
	}
}

func TestPeerIDFromNodeConfig_missingOrUnparsable(t *testing.T) {
	for name, data := range map[string]string{
		"no node section": "database:\n  http_adv_address: \"10.0.0.1:10100\"\n",
		"empty id":        "node:\n  id: \"\"\n",
		"empty file":      "",
		"not yaml":        "node: [\n",
	} {
		if _, err := peerIDFromNodeConfig([]byte(data), "node.yaml"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
