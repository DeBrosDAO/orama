package gateway

import (
	"strings"
	"testing"
)

// A namespace gateway's peers dial it, so it listens — on this node's
// WireGuard IP, which is the host of the rqlite it was pointed at.
func TestLibp2pListenAddrs_namespaceGatewayListensOnItsWireGuardIP(t *testing.T) {
	for name, cfg := range map[string]Config{
		"credentials in the config": {ClientNamespace: "anchat", RQLiteDSN: "http://10.0.0.7:10200", RQLiteUsername: "orama", RQLitePassword: "pw"},
		"credentials in the DSN":    {ClientNamespace: "anchat", RQLiteDSN: "http://orama:pw@10.0.0.7:10200"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := libp2pListenAddrs(&cfg)
			if err != nil {
				t.Fatalf("libp2pListenAddrs: %v", err)
			}
			if len(got) != 1 || got[0] != "/ip4/10.0.0.7/tcp/0" {
				t.Fatalf("got %v, want [/ip4/10.0.0.7/tcp/0]", got)
			}
		})
	}
}

// The index and lobby gateways run no peer discovery; nothing dials them, so
// they have no listener.
func TestLibp2pListenAddrs_otherGatewaysDoNotListen(t *testing.T) {
	for _, ns := range []string{"index", "default", ""} {
		cfg := Config{ClientNamespace: ns, RQLiteDSN: "http://10.0.0.7:10100"}
		got, err := libp2pListenAddrs(&cfg)
		if err != nil || got != nil {
			t.Errorf("namespace %q: got %v, %v; want no listener", ns, got, err)
		}
	}
}

// The address is read, not guessed: a DSN that does not name an overlay
// address is an error, never a fallback to every interface.
func TestLibp2pListenAddrs_refusesADSNThatIsNotTheOverlay(t *testing.T) {
	for dsn, want := range map[string]string{
		"":                          "rqlite_dsn",
		"http://rqlite.local:10200": "not an IP address",
		"http://127.0.0.1:10200":    "not on the WireGuard overlay",
		"http://0.0.0.0:10200":      "wildcard",
		"http://217.76.56.2:10200":  "not on the WireGuard overlay",
		"http://[fd00::7]:10200":    "not on the WireGuard overlay",
	} {
		cfg := Config{ClientNamespace: "anchat", RQLiteDSN: dsn, RQLiteUsername: "orama", RQLitePassword: "pw"}
		got, err := libp2pListenAddrs(&cfg)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("dsn %q: got %v, %v; want an error containing %q", dsn, got, err, want)
		}
	}
}
