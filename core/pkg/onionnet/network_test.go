package onionnet

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testOnion = "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"
	hex40a    = "0123456789abcdef0123456789abcdef01234567"
	hex40b    = "fedcba9876543210fedcba9876543210fedcba98"
)

func testNetwork() Network {
	n := Network{Name: "stagenet", Private: true, ValidatorOnions: []string{testOnion + ":80"}}
	for i, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		n.Authorities = append(n.Authorities, Authority{
			Nickname: "auth" + string(rune('a'+i)), Address: ip + ":31021", ORPort: 31020,
			V3Ident: strings.Repeat(string(rune('a'+i)), 40), Fingerprint: hex40a,
		})
	}
	n.Fallbacks = []Fallback{{Address: "198.51.100.7:31021", ORPort: 31020, ID: hex40b}}
	return n
}

func writeNetwork(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "network.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_roundTrip(t *testing.T) {
	got, err := Load(writeNetwork(t, testNetwork()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "stagenet" || len(got.Authorities) != 3 || len(got.Fallbacks) != 1 || len(got.ValidatorOnions) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestLoad_refusals(t *testing.T) {
	mutate := map[string]func(*Network){
		"a public network is not launched": func(n *Network) { n.Private = false },
		"two authorities are too few":      func(n *Network) { n.Authorities = n.Authorities[:2] },
		"an empty name":                    func(n *Network) { n.Name = "" },
		"a name that is a path":            func(n *Network) { n.Name = "../x" },
		"a nickname with a newline":        func(n *Network) { n.Authorities[0].Nickname = "a\nSocksPort 9999" },
		"an authority by name":             func(n *Network) { n.Authorities[0].Address = "dirauth.example.com:80" },
		"an authority without a port":      func(n *Network) { n.Authorities[0].Address = "192.0.2.1" },
		"a v3ident that is short":          func(n *Network) { n.Authorities[0].V3Ident = "abcd" },
		"a fingerprint that is not hex":    func(n *Network) { n.Authorities[0].Fingerprint = strings.Repeat("z", 40) },
		"the same authority twice":         func(n *Network) { n.Authorities[1].V3Ident = n.Authorities[0].V3Ident },
		"orport 0":                         func(n *Network) { n.Authorities[0].ORPort = 0 },
		"a fallback by name":               func(n *Network) { n.Fallbacks[0].Address = "relay.example.com:443" },
		"an authority with an IPv6 zone":   func(n *Network) { n.Authorities[0].Address = "[fe80::1%a\nClientTransportPlugin x exec /bin/sh]:80" },
		"a fallback with an IPv6 zone":     func(n *Network) { n.Fallbacks[0].Address = "[fe80::1%a\nControlPort 1]:80" },
		"an authority on IPv6":             func(n *Network) { n.Authorities[0].Address = "[2001:db8::1]:31021" },
		"a clearnet host as a validator":   func(n *Network) { n.ValidatorOnions = []string{"chain.example.com"} },
	}
	for name, fn := range mutate {
		n := testNetwork()
		fn(&n)
		_, err := Load(writeNetwork(t, n))
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	_, err := Load(writeNetwork(t, Network{Name: "x"}))
	if !errors.Is(err, ErrPublicNetwork) {
		t.Errorf("not marked private: %v", err)
	}
}

func TestLoad_unknownFieldsMissingFileAndOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.json")
	if err := os.WriteFile(path, []byte(`{"name":"x","private":true,"authorities":[],"extra":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("unknown field: %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("a missing file was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", maxNetworkFile+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversize: %v", err)
	}
}

func TestRandomValidatorOnion(t *testing.T) {
	n := testNetwork()
	n.ValidatorOnions = nil
	if _, err := n.RandomValidatorOnion(); !errors.Is(err, ErrNoValidatorOnion) {
		t.Fatalf("empty list: %v", err)
	}
	n.ValidatorOnions = []string{"a", "b", "c", "d"}
	seen := map[string]bool{}
	for i := 0; i < 400; i++ {
		o, err := n.RandomValidatorOnion()
		if err != nil {
			t.Fatal(err)
		}
		seen[o] = true
	}
	if len(seen) != 4 {
		t.Fatalf("400 picks reached only %v of 4 validators", seen)
	}
}

func TestTorrc(t *testing.T) {
	rc, err := testNetwork().Torrc(Options{DataDir: "/tmp/tor-data", SocksAddr: "127.0.0.1:9150", DNSAddr: "127.0.0.1:9153"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"DataDirectory /tmp/tor-data\n",
		"SocksPort 127.0.0.1:9150 IsolateSOCKSAuth\n",
		"DNSPort 127.0.0.1:9153\n",
		"ClientOnly 1\n", "ORPort 0\n", "ExitRelay 0\n", "UseDefaultFallbackDirs 0\n",
		"DirAuthority autha orport=31020 v3ident=" + strings.Repeat("a", 40) + " 192.0.2.1:31021 " + hex40a + "\n",
		"FallbackDir 198.51.100.7:31021 orport=31020 id=" + hex40b + "\n",
	} {
		if !strings.Contains(rc, want) {
			t.Errorf("torrc lacks %q:\n%s", want, rc)
		}
	}
	if strings.Count(rc, "DirAuthority ") != 3 {
		t.Errorf("want exactly the network's three authorities:\n%s", rc)
	}
	if strings.Contains(rc, "DNSPort") != true {
		t.Error("DNSPort missing")
	}
	plain, err := testNetwork().Torrc(Options{DataDir: "/tmp/tor-data", SocksAddr: "127.0.0.1:9150"})
	if err != nil || strings.Contains(plain, "DNSPort") {
		t.Errorf("no DNS address means no DNSPort: %v\n%s", err, plain)
	}
}

func TestTorrc_refusesWhatCouldInjectOrExpose(t *testing.T) {
	bad := []Options{
		{DataDir: "relative/dir", SocksAddr: "127.0.0.1:9150"},
		{DataDir: "/tmp/a b", SocksAddr: "127.0.0.1:9150"},
		{DataDir: "/tmp/a\nSocksPort 0.0.0.0:1", SocksAddr: "127.0.0.1:9150"},
		{DataDir: "/tmp/x", SocksAddr: "0.0.0.0:9150"},
		{DataDir: "/tmp/x", SocksAddr: "example.com:9150"},
		{DataDir: "/tmp/x", SocksAddr: "127.0.0.1:0"},
		{DataDir: "/tmp/x", SocksAddr: "127.0.0.1:9150", DNSAddr: "10.0.0.1:53"},
		// netip accepts any text as an IPv6 zone and calls ::1%zone loopback; the
		// zone would add lines to the torrc.
		{DataDir: "/tmp/x", SocksAddr: "[::1%a\nClientTransportPlugin x exec /bin/sh]:9150"},
		{DataDir: "/tmp/x", SocksAddr: "127.0.0.1:9150", DNSAddr: "[::1%a\nControlPort 9051]:53"},
	}
	for _, o := range bad {
		if _, err := testNetwork().Torrc(o); err == nil {
			t.Errorf("%+v was accepted", o)
		}
	}
	n := testNetwork()
	n.Private = false
	if _, err := n.Torrc(Options{DataDir: "/tmp/x", SocksAddr: "127.0.0.1:9150"}); !errors.Is(err, ErrPublicNetwork) {
		t.Errorf("a torrc for a public network: %v", err)
	}
}

func TestDefaultDataDir(t *testing.T) {
	d, err := DefaultDataDir("stagenet")
	if err != nil || !strings.HasSuffix(d, filepath.Join("orama", "onion", "stagenet")) {
		t.Fatalf("got %q, %v", d, err)
	}
	if _, err := DefaultDataDir("../etc"); err == nil {
		t.Error("a path as a network name was accepted")
	}
}
