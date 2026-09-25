package report

import (
	"encoding/json"
	"testing"
)

func TestParseTorBootstrap_lastLineWins(t *testing.T) {
	log := "Bootstrapped 0% (starting): Starting\n" +
		"Bootstrapped 45% (requesting_descriptors): Asking for relay descriptors\n" +
		"Bootstrapped 100% (done): Done\n"
	if got := parseTorBootstrap(log); got != 100 {
		t.Errorf("parseTorBootstrap = %d, want 100", got)
	}
}

func TestParseTorBootstrap_stillConnecting(t *testing.T) {
	if got := parseTorBootstrap("Bootstrapped 10% (conn_done): Connected to a relay\n"); got != 10 {
		t.Errorf("parseTorBootstrap = %d, want 10", got)
	}
}

// No line is not the same as 0%: the journal may simply have been vacuumed.
func TestParseTorBootstrap_noLineIsUnknown(t *testing.T) {
	for _, log := range []string{"", "Tor 0.4.8.x running on Linux\n"} {
		if got := parseTorBootstrap(log); got != torBootstrapUnknown {
			t.Errorf("parseTorBootstrap(%q) = %d, want unknown (%d)", log, got, torBootstrapUnknown)
		}
	}
}

// The monitor reads the node report as JSON; the key is part of that contract.
func TestNodeReport_torJSONKey(t *testing.T) {
	data, err := json.Marshal(NodeReport{Tor: &TorReport{BootstrapPct: 100, Bootstrapped: true}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["tor"]; !ok {
		t.Errorf("node report JSON has no \"tor\" key: %s", data)
	}
	if _, ok := decoded["anyone"]; ok {
		t.Error("the removed \"anyone\" key must not be emitted")
	}
}
