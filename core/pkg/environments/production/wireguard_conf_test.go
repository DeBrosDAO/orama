package production

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConf = `# WireGuard mesh configuration (managed by Orama Network)
[Interface]
PrivateKey = aPrivateKeyValue=
Address = 10.0.0.1/24
ListenPort = 51820
MTU = 1420
PostUp = iptables -I INPUT 1 -i wg0 -s 10.0.0.0/24 -j ACCEPT
PostDown = iptables -D INPUT -i wg0 -s 10.0.0.0/24 -j ACCEPT

[Peer]
PublicKey = peerOneKey=
Endpoint = 203.0.113.1:51820
AllowedIPs = 10.0.0.2/32
PersistentKeepalive = 25
`

func writeConf(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "wg0.conf")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed conf: %v", err)
	}
	return path
}

// The [Interface] block carries the private key and the wg-quick-only
// directives. This process cannot reconstruct either, so a rewrite must leave
// the block byte-identical.
func TestPersistPeersPreservesInterfaceBlock(t *testing.T) {
	path := writeConf(t, sampleConf)
	c := NewWGConf(path)

	err := c.PersistPeers([]WireGuardPeer{
		{PublicKey: "peerTwoKey=", Endpoint: "203.0.113.2:51820", AllowedIP: "10.0.0.3/32"},
	})
	if err != nil {
		t.Fatalf("PersistPeers: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	out := string(got)

	for _, must := range []string{
		"PrivateKey = aPrivateKeyValue=",
		"Address = 10.0.0.1/24",
		"ListenPort = 51820",
		"MTU = 1420",
		"PostUp = iptables",
		"PostDown = iptables",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("rewritten conf lost %q:\n%s", must, out)
		}
	}
	if !strings.Contains(out, "PublicKey = peerTwoKey=") {
		t.Errorf("new peer missing:\n%s", out)
	}
	if strings.Contains(out, "peerOneKey=") {
		t.Errorf("replaced peer still present:\n%s", out)
	}
}

// Removing every peer is legitimate (a node alone in the mesh) and must not
// damage the interface block.
func TestPersistPeersWithEmptySet(t *testing.T) {
	path := writeConf(t, sampleConf)
	if err := NewWGConf(path).PersistPeers(nil); err != nil {
		t.Fatalf("PersistPeers: %v", err)
	}
	out, _ := os.ReadFile(path)
	if strings.Contains(string(out), "[Peer]") {
		t.Errorf("expected no peers:\n%s", out)
	}
	if !strings.Contains(string(out), "PrivateKey = aPrivateKeyValue=") {
		t.Errorf("interface block damaged:\n%s", out)
	}
}

// An unchanged mesh must render byte-identically, so a diff only ever shows a
// real membership change.
func TestPersistPeersIsStableAcrossOrderings(t *testing.T) {
	a := writeConf(t, sampleConf)
	b := writeConf(t, sampleConf)

	peers := []WireGuardPeer{
		{PublicKey: "k2=", Endpoint: "203.0.113.2:51820", AllowedIP: "10.0.0.3/32"},
		{PublicKey: "k1=", Endpoint: "203.0.113.1:51820", AllowedIP: "10.0.0.2/32"},
	}
	reversed := []WireGuardPeer{peers[1], peers[0]}

	if err := NewWGConf(a).PersistPeers(peers); err != nil {
		t.Fatalf("persist a: %v", err)
	}
	if err := NewWGConf(b).PersistPeers(reversed); err != nil {
		t.Fatalf("persist b: %v", err)
	}
	ca, _ := os.ReadFile(a)
	cb, _ := os.ReadFile(b)
	if string(ca) != string(cb) {
		t.Errorf("same peer set rendered differently:\n--- a ---\n%s\n--- b ---\n%s", ca, cb)
	}
}

// bugboard #247: the file holds a private key, so the mode is set explicitly
// rather than left to the umask.
func TestPersistPeersWritesPrivateMode(t *testing.T) {
	path := writeConf(t, sampleConf)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := NewWGConf(path).PersistPeers(nil); err != nil {
		t.Fatalf("PersistPeers: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

// A conf we cannot understand must be left alone. Rewriting a file whose
// private key we cannot see would take the interface down at next boot.
func TestPersistPeersRefusesUnusableConf(t *testing.T) {
	cases := map[string]string{
		"no interface section":            "[Peer]\nPublicKey = x=\n",
		"interface without a private key": "[Interface]\nAddress = 10.0.0.1/24\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeConf(t, body)
			before, _ := os.ReadFile(path)
			if err := NewWGConf(path).PersistPeers([]WireGuardPeer{{PublicKey: "k=", AllowedIP: "10.0.0.2/32"}}); err == nil {
				t.Fatal("expected a refusal")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Errorf("refused write still modified the file:\n%s", after)
			}
		})
	}
}

func TestPersistPeersMissingFile(t *testing.T) {
	if err := NewWGConf(filepath.Join(t.TempDir(), "absent.conf")).PersistPeers(nil); err == nil {
		t.Fatal("expected an error for a missing conf")
	}
}

// `wg show <iface> dump` is the only machine-readable source of a peer's
// endpoint and allowed IPs, which is what makes drift detectable.
func TestParseWGDump(t *testing.T) {
	dump := strings.Join([]string{
		"privKey=\tpubKey=\t51820\toff",
		"peerA=\t(none)\t203.0.113.1:51820\t10.0.0.2/32\t1712345678\t100\t200\t25",
		"peerB=\t(none)\t(none)\t10.0.0.3/32\t0\t0\t0\t25",
	}, "\n")

	peers := parseWGDump(dump)
	if len(peers) != 2 {
		t.Fatalf("parsed %d peers, want 2: %+v", len(peers), peers)
	}
	a, ok := peers["peerA="]
	if !ok {
		t.Fatal("peerA missing")
	}
	if a.Endpoint != "203.0.113.1:51820" || a.AllowedIP != "10.0.0.2/32" {
		t.Errorf("peerA = %+v", a)
	}
	// A peer never contacted has no endpoint; "(none)" must not leak through as
	// a literal address.
	if b := peers["peerB="]; b.Endpoint != "" {
		t.Errorf("peerB endpoint = %q, want empty", b.Endpoint)
	}
}

func TestParseWGDumpIgnoresGarbage(t *testing.T) {
	if got := parseWGDump(""); len(got) != 0 {
		t.Errorf("empty dump produced %d peers", len(got))
	}
	if got := parseWGDump("only-interface-line"); len(got) != 0 {
		t.Errorf("interface-only dump produced %d peers", len(got))
	}
	if got := parseWGDump("iface\ntruncated\tline"); len(got) != 0 {
		t.Errorf("truncated peer line produced %d peers", len(got))
	}
}
