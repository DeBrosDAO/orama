package globalhealth

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func boolp(v bool) *bool        { return &v }
func floatp(v float64) *float64 { return &v }
func intp(v int) *int           { return &v }
func int64p(v int64) *int64     { return &v }

func chain(height int64, peers int, validators int) *report.ChainReport {
	c := &report.ChainReport{
		ServiceActive: true, UnitState: "active", Responsive: true,
		LatestHeight: height, Peers: peers, ValidatorCount: validators,
		BlockAgeSec: 1, MinSignedPerWindow: floatp(0.9), MissedBlockRatio: floatp(0),
		Jailed: boolp(false), Tombstoned: boolp(false),
	}
	return c
}

func issuesFor(t *testing.T, nodes ...Node) []Issue {
	t.Helper()
	return Evaluate(nodes)
}

func has(issues []Issue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func TestEvaluate_healthy(t *testing.T) {
	n := Node{Host: "10.0.0.1", Chain: chain(100, 2, 3)}
	if got := issuesFor(t, n); len(got) != 0 {
		t.Fatalf("healthy chain alerted: %+v", got)
	}
}

func TestEvaluate_lagging(t *testing.T) {
	nodes := []Node{
		{Host: "a", Chain: chain(100, 2, 3)},
		{Host: "b", Chain: chain(100, 2, 3)},
		{Host: "c", Chain: chain(70, 2, 3)},
	}
	got := issuesFor(t, nodes...)
	if len(got) != 1 || got[0].Code != "chain.lag" || got[0].Host != "c" || got[0].Severity != Warning {
		t.Fatalf("got %+v", got)
	}
}

func TestEvaluate_catchingUpDoesNotLag(t *testing.T) {
	behind := chain(70, 2, 3)
	behind.CatchingUp = true
	nodes := []Node{{Host: "a", Chain: chain(100, 2, 3)}, {Host: "b", Chain: chain(100, 2, 3)}, {Host: "c", Chain: behind}}
	if got := issuesFor(t, nodes...); len(got) != 0 {
		t.Fatalf("a catching-up node alerted: %+v", got)
	}
}

func TestEvaluate_jailed(t *testing.T) {
	c := chain(100, 2, 3)
	c.Jailed = boolp(true)
	got := issuesFor(t, Node{Host: "a", Chain: c})
	if len(got) != 1 || got[0].Code != "chain.jailed" || got[0].Severity != Critical {
		t.Fatalf("got %+v", got)
	}
}

func TestEvaluate_missingPeers(t *testing.T) {
	c := chain(100, 0, 3)
	got := issuesFor(t, Node{Host: "a", Chain: c})
	if !has(got, "chain.peers") {
		t.Fatalf("got %+v", got)
	}
	alone := chain(100, 0, 1)
	if got := issuesFor(t, Node{Host: "a", Chain: alone}); len(got) != 0 {
		t.Fatalf("a single validator with no peers alerted: %+v", got)
	}
}

func TestEvaluate_lowBalanceAndProofMisses(t *testing.T) {
	g := &report.GlobalReport{
		Units: []report.GlobalUnit{{Name: constants.GlobalProviderUnit, State: "active"}},
		Provider: &report.ProviderReport{
			HotKeyBalanceNorama: int64p(0),
			ProofMisses:         intp(2),
			DiskBytes:           int64p(5),
			StorageMaxBytes:     int64p(4),
		},
	}
	got := issuesFor(t, Node{Host: "a", Global: g})
	if !has(got, "global.provider.balance") || !has(got, "global.provider.proofs") || !has(got, "global.provider.disk") {
		t.Fatalf("got %+v", got)
	}
	for _, i := range got {
		if i.Code == "global.provider.balance" && i.Severity != Critical {
			t.Fatalf("balance severity %s", i.Severity)
		}
	}
}

func TestEvaluate_absentMonitorDoesNotAlert(t *testing.T) {
	g := &report.GlobalReport{
		Units:    []report.GlobalUnit{{Name: constants.GlobalProviderUnit, State: "active"}},
		Provider: &report.ProviderReport{},
	}
	if got := issuesFor(t, Node{Host: "a", Global: g}); len(got) != 0 {
		t.Fatalf("an empty provider status alerted: %+v", got)
	}
}

func TestEvaluate_publicKuboOverStorageMax(t *testing.T) {
	g := &report.GlobalReport{
		Units:      []report.GlobalUnit{{Name: constants.GlobalIPFSUnit, State: "active"}},
		PublicIPFS: &report.PublicIPFSReport{RepoBytes: 11, StorageMaxBytes: 10},
	}
	got := issuesFor(t, Node{Host: "a", Global: g})
	if len(got) != 1 || got[0].Code != "global.ipfs.disk" {
		t.Fatalf("got %+v", got)
	}
}

func TestEvaluate_relayNotInConsensus(t *testing.T) {
	g := &report.GlobalReport{
		Units: []report.GlobalUnit{{Name: constants.GlobalTorRelayUnit, State: "active"}},
		Relay: &report.RelayReport{InConsensus: boolp(false)},
	}
	got := issuesFor(t, Node{Host: "a", Global: g})
	if len(got) != 1 || got[0].Code != "global.relay.consensus" || !strings.Contains(got[0].Message, "relay set") {
		t.Fatalf("got %+v", got)
	}
}

func TestEvaluate_directoryAuthorityNotInConsensus(t *testing.T) {
	g := &report.GlobalReport{
		Units: []report.GlobalUnit{{Name: constants.GlobalTorDirauthUnit, State: "active"}},
		Relay: &report.RelayReport{InConsensus: boolp(false)},
	}
	got := issuesFor(t, Node{Host: "a", Global: g})
	if len(got) != 1 || got[0].Code != "global.dirauth.consensus" || got[0].Severity != Warning || !strings.Contains(got[0].Message, "directory authority") {
		t.Fatalf("got %+v", got)
	}
	g.Relay = &report.RelayReport{InConsensus: boolp(true)}
	if got := issuesFor(t, Node{Host: "a", Global: g}); len(got) != 0 {
		t.Fatalf("an authority in the consensus raised %+v", got)
	}
	g.Relay = &report.RelayReport{}
	if got := issuesFor(t, Node{Host: "a", Global: g}); len(got) != 0 {
		t.Fatalf("an authority that cannot say raised %+v", got)
	}
	g.Units[0].State = "failed"
	g.Relay = &report.RelayReport{InConsensus: boolp(false)}
	got = issuesFor(t, Node{Host: "a", Global: g})
	if len(got) != 1 || got[0].Code != "global.dirauth.down" || got[0].Severity != Critical {
		t.Fatalf("a failed authority unit: %+v", got)
	}
}

func TestEvaluate_failedUnit(t *testing.T) {
	c := &report.ChainReport{ServiceActive: false, UnitState: "failed"}
	got := issuesFor(t, Node{Host: "a", Chain: c})
	if len(got) != 1 || got[0].Severity != Critical || got[0].Code != "chain.down" {
		t.Fatalf("got %+v", got)
	}
}

func TestEvaluate_missedBlocks(t *testing.T) {
	c := chain(100, 2, 3)
	c.MissedBlockRatio = floatp(0.06)
	c.MinSignedPerWindow = floatp(0.9)
	got := issuesFor(t, Node{Host: "a", Chain: c})
	if len(got) != 1 || got[0].Severity != Warning || got[0].Code != "chain.missed" {
		t.Fatalf("half threshold: %+v", got)
	}
	c.MissedBlockRatio = floatp(0.1)
	got = issuesFor(t, Node{Host: "a", Chain: c})
	if len(got) != 1 || got[0].Severity != Critical {
		t.Fatalf("at threshold: %+v", got)
	}
}
