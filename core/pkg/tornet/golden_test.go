package tornet

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update-torrc-golden", false, "rewrite testdata/torrc-*.golden from the renderers")

// The golden torrc files are the templates plans/open-network/decisions/E0.md
// hands the installer: one per role, rendered for the test network. A change to
// a renderer shows up as a diff in review, and fails here until the files are
// rewritten with `go test ./pkg/tornet -run Golden -update-torrc-golden`.
func TestTorrcGolden(t *testing.T) {
	network := testNetwork()
	network.AllowExit = true
	relay := relayConfig()
	relay.Network = network
	relay.BandwidthMbit = 100
	exit := relay
	exit.Exit = true
	exit.ExitReject = []string{"203.0.113.9:*"}
	dirauth := relay
	dirauth.Home = "/var/lib/orama-global/tor-dirauth"
	dirauth.Authority = true
	dirauth.DirPort = 31021
	dirauth.Nickname = "OramaAuth1"
	dirauth.Address = "57.129.166.16"

	onion, err := OnionTorrc(OnionConfig{Network: network, Home: "/var/lib/orama-global/tor-onion", Target: "127.0.0.1:31022"})
	if err != nil {
		t.Fatal(err)
	}
	client, err := ClientTorrc(ClientConfig{Network: network, Home: "/var/lib/orama-tornet", SOCKSAddr: "127.0.0.1:9052"})
	if err != nil {
		t.Fatal(err)
	}
	rendered := map[string]string{"onion": onion, "client": client}
	for name, c := range map[string]RelayConfig{"relay": relay, "exit": exit, "dirauth": dirauth} {
		out, err := RelayTorrc(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rendered[name] = out
	}
	for name, got := range rendered {
		path := filepath.Join("testdata", "torrc-"+name+".golden")
		if *updateGolden {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update-torrc-golden)", name, err)
		}
		if string(want) != got {
			t.Errorf("the %s torrc changed:\n--- golden\n%s\n--- rendered\n%s", name, want, got)
		}
	}
}
