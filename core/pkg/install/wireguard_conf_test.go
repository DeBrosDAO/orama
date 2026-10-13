package install

import (
	"strings"
	"testing"
)

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

// "(none)" (an address that moved to a new key) and CIDR lists are not mesh
// peers; persisting them would be refused as a whole by the helper.
func TestParseWGDump_SkipsPeersWithoutASingleAddress(t *testing.T) {
	dump := "priv\tpub\t51820\toff\n" +
		"good=\t(none)\t203.0.113.2:51820\t10.0.0.2/32\t0\t0\t0\t25\n" +
		"moved=\t(none)\t203.0.113.3:51820\t(none)\t0\t0\t0\t25\n" +
		"multi=\t(none)\t203.0.113.4:51820\t10.0.0.4/32,10.0.0.5/32\t0\t0\t0\t25\n"
	peers := parseWGDump(dump)
	if len(peers) != 1 || peers["good="].AllowedIP != "10.0.0.2/32" {
		t.Fatalf("got %+v", peers)
	}
}
