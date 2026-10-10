package tornet

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testOnionA = "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"
	testOnionB = "bcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwxy.onion"
)

func writeNetworkFile(t *testing.T, n Network) string {
	t.Helper()
	body, err := n.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tor-network.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_roundTripWithValidatorOnions(t *testing.T) {
	n := testNetwork()
	n.ValidatorOnions = []string{testOnionA + ":80", testOnionB}
	got, err := Load(writeNetworkFile(t, n))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Private || got.Name != "orama-teststage" || len(got.Authorities) != 3 || len(got.ValidatorOnions) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestLoad_refusesWhatEveryClientMustRefuse(t *testing.T) {
	mutate := map[string]func(*Network){
		"two authorities are too few":      func(n *Network) { n.Authorities = n.Authorities[:2] },
		"an empty name":                    func(n *Network) { n.Name = "" },
		"a name that is a path":            func(n *Network) { n.Name = "../x" },
		"a nickname with a newline":        func(n *Network) { n.Authorities[0].Nickname = "a\nSocksPort 9999" },
		"an authority by name":             func(n *Network) { n.Authorities[0].Address = "dirauth.example.com" },
		"an authority with a port":         func(n *Network) { n.Authorities[0].Address = "57.129.166.16:31021" },
		"a v3ident that is short":          func(n *Network) { n.Authorities[0].V3Ident = "ABCD" },
		"a fingerprint that is not hex":    func(n *Network) { n.Authorities[0].Fingerprint = strings.Repeat("Z", 40) },
		"the same authority twice":         func(n *Network) { n.Authorities[1].V3Ident = n.Authorities[0].V3Ident },
		"orport 0":                         func(n *Network) { n.Authorities[0].ORPort = 0 },
		"an authority with an IPv6 zone":   func(n *Network) { n.Authorities[0].Address = "fe80::1%a\nClientTransportPlugin x exec /bin/sh" },
		"an authority on IPv6":             func(n *Network) { n.Authorities[0].Address = "2606:4700::1111" },
		"an authority with a newline":      func(n *Network) { n.Authorities[0].Address = "57.129.166.16\nControlPort 9051" },
		"a clearnet host as a validator":   func(n *Network) { n.ValidatorOnions = []string{"chain.example.com"} },
		"a validator onion with a newline": func(n *Network) { n.ValidatorOnions = []string{testOnionA + "\nControlPort 1"} },
		"a validator onion listed twice":   func(n *Network) { n.ValidatorOnions = []string{testOnionA, testOnionA} },
		"too many validator onions":        func(n *Network) { n.ValidatorOnions = make([]string, maxValidatorOnions+1) },
	}
	for name, fn := range mutate {
		n := testNetwork()
		fn(&n)
		// Marshal validates, so write the refused file by hand.
		body, err := marshalUnchecked(n)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "tor-network.json")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestLoad_aPublicNetworkIsNotLaunched(t *testing.T) {
	n := testNetwork()
	n.Private = false
	body, err := marshalUnchecked(n)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tor-network.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); !errors.Is(err, ErrPublicNetwork) {
		t.Fatalf("not marked private: %v", err)
	}
	if _, err := n.Marshal(); !errors.Is(err, ErrPublicNetwork) {
		t.Fatalf("Marshal of a public network: %v", err)
	}
}

func TestLoad_unknownFieldsMissingFileAndOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tor-network.json")
	if err := os.WriteFile(path, []byte(`{"name":"orama-x","private":true,"fallbacks":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("unknown field: %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("a missing file was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", NetworkFileLimit+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversize: %v", err)
	}
}

func TestNetwork_validatorOnionsAreLowerCasedOnParse(t *testing.T) {
	n := testNetwork()
	n.ValidatorOnions = []string{testOnionA}
	body, err := n.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	upper := strings.Replace(string(body), testOnionA, strings.ToUpper(testOnionA), 1)
	got, err := ParseNetwork([]byte(upper))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ValidatorOnions) != 1 || got.ValidatorOnions[0] != testOnionA {
		t.Fatalf("onions = %v", got.ValidatorOnions)
	}
}

func TestWithValidatorOnions(t *testing.T) {
	n := testNetwork()
	got, err := n.WithValidatorOnions(strings.ToUpper(testOnionA), testOnionB+":31003")
	if err != nil {
		t.Fatal(err)
	}
	again, err := got.WithValidatorOnions(testOnionA)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.ValidatorOnions) != 2 || again.ValidatorOnions[0] != testOnionA {
		t.Fatalf("onions = %v", again.ValidatorOnions)
	}
	if len(n.ValidatorOnions) != 0 {
		t.Fatal("the receiver was modified")
	}
	if _, err := n.WithValidatorOnions("chain.example.com"); err == nil {
		t.Fatal("a clearnet host was added as a validator onion")
	}
	if _, err := n.WithValidatorOnions(); err != nil {
		t.Fatalf("adding nothing: %v", err)
	}
}

func TestRandomValidatorOnion(t *testing.T) {
	n := testNetwork()
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

func TestValidNetworkName(t *testing.T) {
	for name, want := range map[string]bool{"stagenet": true, "orama-teststage": true, "../etc": false, "": false, "Up": false, "a b": false} {
		if got := ValidNetworkName(name); got != want {
			t.Errorf("ValidNetworkName(%q) = %t", name, got)
		}
	}
}

// marshalUnchecked encodes a network without validating it, to write the files
// Load must refuse.
func marshalUnchecked(n Network) ([]byte, error) { return json.Marshal(n) }

func TestAddValidatorOnionsToFile(t *testing.T) {
	path := writeNetworkFile(t, testNetwork())
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	n, added, err := AddValidatorOnionsToFile(path, testOnionA, testOnionB+":31003")
	if err != nil || added != 2 || len(n.ValidatorOnions) != 2 {
		t.Fatalf("added %d, %v, %v", added, n.ValidatorOnions, err)
	}
	onDisk, err := Load(path)
	if err != nil || len(onDisk.ValidatorOnions) != 2 || onDisk.ValidatorOnions[1] != testOnionB+":31003" {
		t.Fatalf("file = %+v, %v", onDisk, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Errorf("mode %o", info.Mode().Perm())
	}
	if _, added, err := AddValidatorOnionsToFile(path, strings.ToUpper(testOnionA)); err != nil || added != 0 {
		t.Errorf("an onion already listed: added %d, %v", added, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("a temporary file was left behind: %v", entries)
	}
}

func TestAddValidatorOnionsToFile_refusalsLeaveTheFileAlone(t *testing.T) {
	path := writeNetworkFile(t, testNetwork())
	before, _ := os.ReadFile(path)
	if _, _, err := AddValidatorOnionsToFile(path, "chain.example.com"); err == nil {
		t.Fatal("a clearnet host was added")
	}
	if _, _, err := AddValidatorOnionsToFile(filepath.Join(t.TempDir(), "absent.json"), testOnionA); err == nil {
		t.Fatal("a missing file was accepted")
	}
	bad := filepath.Join(t.TempDir(), "tor-network.json")
	if err := os.WriteFile(bad, []byte(`{"name":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddValidatorOnionsToFile(bad, testOnionA); err == nil {
		t.Fatal("an invalid network file was rewritten")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("the file changed on a refused add")
	}
}
