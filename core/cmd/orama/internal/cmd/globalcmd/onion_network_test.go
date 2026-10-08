package globalcmd

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/onionnet"
)

const secondOnion = "bbcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"

func networkFile(t *testing.T, onions ...string) string {
	t.Helper()
	n := onionnet.Network{Name: "stagenet", Private: true, ValidatorOnions: onions}
	for i, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		n.Authorities = append(n.Authorities, onionnet.Authority{
			Nickname: "auth" + string(rune('a'+i)), Address: ip + ":31021", ORPort: 31020,
			V3Ident: strings.Repeat(string(rune('a'+i)), 40), Fingerprint: strings.Repeat("0", 40),
		})
	}
	body, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "network.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeTor is a stand-in tor that reports it bootstrapped, or exits with a
// failure, and listens on no SOCKS port.
func fakeTor(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tor")
	script := "#!/bin/sh\ntrap 'exit 0' INT TERM\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

const bootedTor = "echo 'Bootstrapped 100% (done): Done'\nwhile true; do sleep 0.1; done"

func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{OnionEnv, OnionSOCKSEnv, OnionNetworkEnv} {
		t.Setenv(k, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
}

func TestChainTarget_networkPicksARandomValidatorPerTransaction(t *testing.T) {
	isolateEnv(t)
	file := networkFile(t, testOnion, secondOnion)
	tor := fakeTor(t, bootedTor)
	seen := map[string]bool{}
	for i := 0; i < 24; i++ {
		_, base, done, err := chainTarget(onionCmd(t, "--onion-network", file, "--onion-tor", tor), context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		done()
		seen[base] = true
	}
	if len(seen) != 2 {
		t.Fatalf("24 transactions went to %v; each should pick one of the two validators at random", seen)
	}
	for base := range seen {
		if !strings.HasPrefix(base, "http://") || !strings.HasSuffix(base, ".onion:80") {
			t.Errorf("base %q", base)
		}
	}
}

func TestChainTarget_networkWithAnExplicitOnionUsesIt(t *testing.T) {
	isolateEnv(t)
	file := networkFile(t, testOnion, secondOnion)
	_, base, done, err := chainTarget(onionCmd(t, "--onion-network", file, "--onion", secondOnion+":31003", "--onion-tor", fakeTor(t, bootedTor)), context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if base != "http://"+secondOnion+":31003" {
		t.Fatalf("base = %q", base)
	}
}

func TestChainTarget_networkRefusals(t *testing.T) {
	isolateEnv(t)
	file := networkFile(t, testOnion)
	empty := networkFile(t)
	tor := fakeTor(t, bootedTor)
	cases := map[string]struct {
		args []string
		node string
		want string
	}{
		"a node as a second route":     {[]string{"--onion-network", file}, "http://127.0.0.1:31003", "pass one"},
		"a second Tor client":          {[]string{"--onion-network", file, "--onion-socks", "127.0.0.1:9150"}, "", "two Tor clients"},
		"no validator to submit to":    {[]string{"--onion-network", empty, "--onion-tor", tor}, "", "validator onion"},
		"a network file that is gone":  {[]string{"--onion-network", filepath.Join(t.TempDir(), "absent.json")}, "", "--onion-network"},
		"a clearnet host as the onion": {[]string{"--onion-network", file, "--onion", "chain.example.com", "--onion-tor", tor}, "", "onion"},
	}
	for name, c := range cases {
		_, _, _, err := chainTarget(onionCmd(t, c.args...), context.Background(), c.node)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestChainTarget_networkThatCannotBeJoinedIsAnErrorNotAClearnetSend(t *testing.T) {
	isolateEnv(t)
	file := networkFile(t, testOnion)
	tor := fakeTor(t, "echo '[warn] no authority answered'\nexit 3")
	_, _, _, err := chainTarget(onionCmd(t, "--onion-network", file, "--onion-tor", tor), context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "the transaction was not sent") || !strings.Contains(err.Error(), "no authority answered") {
		t.Fatalf("err = %v", err)
	}
}

func TestChainTarget_environmentSuppliesTheNetwork(t *testing.T) {
	isolateEnv(t)
	t.Setenv(OnionNetworkEnv, networkFile(t, testOnion))
	_, base, done, err := chainTarget(onionCmd(t, "--onion-tor", fakeTor(t, bootedTor)), context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if base != "http://"+testOnion+":80" {
		t.Fatalf("base = %q", base)
	}
}

// Joined to the network but with the onion service unreachable, the submission
// fails with the Tor error and nothing is sent on the clearnet.
func TestSubmitDirect_networkOnionUnreachableFailsWithoutClearnet(t *testing.T) {
	isolateEnv(t)
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("clearnet request: %s", r.URL)
		return nil, context.Canceled
	})
	defer func() { http.DefaultTransport = old }()

	cmd := onionCmd(t, "--onion-network", networkFile(t, testOnion), "--onion-tor", fakeTor(t, bootedTor))
	cmd.SetContext(context.Background())
	in := clusterreg.Direct{TypeURL: clusterreg.RegisterClusterTypeURL, Msg: []byte{1}, FeeAmount: "1", Gas: 1, ChainID: "orama-test"}
	err := SubmitDirect(cmd, "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s", "", "", 0, 0, in, "registered")
	if err == nil || !strings.Contains(err.Error(), "nothing was tried outside Tor") {
		t.Fatalf("err = %v, want the Tor-unreachable error", err)
	}
}
