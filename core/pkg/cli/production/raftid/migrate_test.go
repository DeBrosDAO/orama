package raftid

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestPlan_needsMigration(t *testing.T) {
	tests := []struct {
		name string
		plan plan
		want bool
	}{
		{"still on its address", plan{CurrentID: "10.0.0.2:10101", PeerID: "12D3KooWAlpha"}, true},
		{"already stable", plan{CurrentID: "12D3KooWAlpha", PeerID: "12D3KooWAlpha"}, false},
		// A run interrupted between the removal and the rejoin leaves a node
		// whose id already matches but which is out of the configuration. It
		// still has work to do, and reporting otherwise is what made the
		// "safe to re-run" promise false.
		{"mid-migration", plan{CurrentID: "12D3KooWAlpha", PeerID: "12D3KooWAlpha", Resume: true}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.plan.NeedsMigration(); got != tc.want {
				t.Fatalf("NeedsMigration = %v, want %v", got, tc.want)
			}
		})
	}
}

// The reset script is the step that cannot be undone, so its shape is pinned.
func TestResetScript_carriesTheIdentityAndTheJoin(t *testing.T) {
	script := resetScript("12D3KooWAlpha", "10.0.0.1:10100")

	// Without -node-id the node comes back under its old address; without
	// -join an empty data directory always bootstraps a solo cluster. Both
	// reach rqlited only through the env file, and omitting the rewrite is
	// what made the node elect itself leader of an empty database.
	for _, want := range []string{
		"-node-id $PEER_ID",
		"JOIN_ARGS=-join $JOIN_ADDR",
		"systemctl stop orama-namespace-rqlite@index.service",
		"systemctl start orama-namespace-rqlite@index.service",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("the reset script is missing %q", want)
		}
	}

	// A pre-existing -node-id must be stripped, not appended to, or a
	// re-migrated node gets two.
	if !strings.Contains(script, `sed 's/-node-id [^ ]*//g'`) {
		t.Fatal("the script must strip any existing -node-id before adding one")
	}

	stopAt := strings.Index(script, "systemctl stop")
	rmAt := strings.Index(script, "rm -rf")
	startAt := strings.Index(script, "systemctl start")
	if !(stopAt < rmAt && rmAt < startAt) {
		t.Fatal("the unit must be stopped before the data directory is removed, and started after")
	}
}

func TestRequireStableIDSupport_namesTheNodesHoldingItBack(t *testing.T) {
	// Migrating while one node is on the old binary makes that node re-add
	// every migrated node as a duplicate voter every five minutes.
	readMarker = func(n inspector.Node) (string, error) {
		if n.Host == "1.2.3.5" {
			return "", nil
		}
		return "12D3KooWAlpha", nil
	}
	t.Cleanup(func() { readMarker = readRemoteMarker })

	err := requireStableIDSupport([]inspector.Node{{Host: "1.2.3.4"}, {Host: "1.2.3.5"}})
	if err == nil {
		t.Fatal("a node with no marker did not block the migration")
	}
	if !strings.Contains(err.Error(), "1.2.3.5") {
		t.Fatalf("the error should name the node holding it back: %v", err)
	}
}

func TestRequireStableIDSupport_passesWhenEveryNodeHasAMarker(t *testing.T) {
	readMarker = func(inspector.Node) (string, error) { return "10.0.0.2:10101", nil }
	t.Cleanup(func() { readMarker = readRemoteMarker })

	if err := requireStableIDSupport([]inspector.Node{{Host: "1.2.3.4"}, {Host: "1.2.3.5"}}); err != nil {
		t.Fatalf("every node reported a marker but the check failed: %v", err)
	}
}

func TestRequireStableIDSupport_anUnreachableNodeBlocks(t *testing.T) {
	// Not knowing is not the same as knowing it is fine.
	readMarker = func(inspector.Node) (string, error) { return "", errUnreachable }
	t.Cleanup(func() { readMarker = readRemoteMarker })

	if err := requireStableIDSupport([]inspector.Node{{Host: "1.2.3.4"}}); err == nil {
		t.Fatal("an unreachable node did not block the migration")
	}
}
