package display

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func intp(v int) *int             { return &v }
func int64p(v int64) *int64       { return &v }
func boolp(v bool) *bool          { return &v }
func float64p(v float64) *float64 { return &v }

func TestGlobalLine_showsDealsAndRelayHealth(t *testing.T) {
	g := &report.GlobalReport{
		Units: []report.GlobalUnit{
			{Name: constants.GlobalIPFSUnit, State: "active"},
			{Name: constants.GlobalProviderUnit, State: "active"},
			{Name: constants.GlobalTorRelayUnit, State: "active"},
		},
		PublicIPFS: &report.PublicIPFSReport{RepoBytes: 5 << 20, StorageMaxBytes: 100 << 20},
		Provider: &report.ProviderReport{HeldSlots: intp(12), PendingSlots: intp(3), ProofMisses: intp(1),
			HotKeyBalanceNorama: int64p(900)},
		Relay: &report.RelayReport{InConsensus: boolp(false)},
	}
	got := globalLine(view.NewTheme(false), g)
	for _, want := range []string{"public Kubo active (5 of 100 MB)", "12 slots held", "3 pending",
		"1 proof misses", "hot key 900 norama", "relay active (not in the relay set)"} {
		if !strings.Contains(got, want) {
			t.Errorf("global line is missing %q: %s", want, got)
		}
	}
}

func TestGlobalLine_absentOrFailedSection(t *testing.T) {
	th := view.NewTheme(false)
	if got := globalLine(th, nil); got != "" {
		t.Fatalf("a node with no global units printed %q", got)
	}
	if got := globalLine(th, &report.GlobalReport{Error: "read orama-global-ipfs.service"}); !strings.Contains(got, "read orama-global-ipfs.service") {
		t.Fatalf("a failed section lost its error: %q", got)
	}
}

func TestGlobalLine_unitWithoutAStatusFile(t *testing.T) {
	g := &report.GlobalReport{
		Units:    []report.GlobalUnit{{Name: constants.GlobalProviderUnit, State: "failed"}},
		Provider: &report.ProviderReport{},
	}
	if got := globalLine(view.NewTheme(false), g); got != "provider failed" {
		t.Fatalf("got %q", got)
	}
}

func TestChainLine_showsValidatorStatus(t *testing.T) {
	th := view.NewTheme(false)
	c := &report.ChainReport{Responsive: true, ChainID: "orama-1", LatestHeight: 9, IsValidator: true, VotingPower: 10,
		Jailed: boolp(true), MissedBlockRatio: float64p(0.25)}
	got := chainLine(th, c)
	for _, want := range []string{"validator (power 10)", "JAILED", "missed 25.00%"} {
		if !strings.Contains(got, want) {
			t.Errorf("chain line is missing %q: %s", want, got)
		}
	}
	c.IsValidator = false
	if got := chainLine(th, c); strings.Contains(got, "validator") {
		t.Fatalf("a full node reads as a validator: %s", got)
	}
}
