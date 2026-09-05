package decommission

import (
	"strings"
	"testing"
)

// The wipe script's two fixes over the one it replaces.
func TestWipeScript(t *testing.T) {
	script := wipeScript(false)

	if !strings.Contains(script, `list-units --all --plain --no-legend "orama-namespace-*"`) {
		t.Error("the script must stop every namespace unit; those are template instances that match no legacy host unit name")
	}
	nsAt := strings.Index(script, "orama-namespace-*")
	rmAt := strings.Index(script, "rm -rf /opt/orama")
	if nsAt < 0 || rmAt < 0 || nsAt > rmAt {
		t.Error("namespace units must be stopped BEFORE the data directory is removed")
	}

	if strings.Contains(script, `pkill -9 -f "ipfs"`) {
		t.Error("an unanchored pkill pattern matches any command line mentioning ipfs")
	}
	if !strings.Contains(script, "/usr/local/bin/ipfs") {
		t.Error("the ipfs pkill pattern must be anchored to a full path")
	}

	if strings.Contains(script, "NUCLEAR=1") {
		t.Error("nuclear must be off unless asked for")
	}
	if !strings.Contains(wipeScript(true), "NUCLEAR=1") {
		t.Error("nuclear must be on when asked for")
	}
}
