package display

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func TestUpgradeJSON_carriesWhetherTheStagedBinaryIsThePlans(t *testing.T) {
	c := &report.ChainReport{LatestHeight: 10, Upgrade: &report.ChainUpgrade{
		Name: "v2", Height: 20, Staged: true, Checksums: report.ChecksumsMismatch, ChecksumProblem: "the staged oramad is not the one the plan names",
	}}
	got := upgradeJSON(c)
	if got == nil || got.Checksums != "mismatch" || got.ChecksumProblem != "the staged oramad is not the one the plan names" {
		t.Fatalf("upgradeJSON = %+v", got)
	}
}

// A staged binary that is not the plan's is the thing to act on: the status table marks it as it
// marks a binary that is not staged, and says which it is.
func TestStatus_aStagedBinaryThatIsNotThePlansIsShownAsMismatching(t *testing.T) {
	snap := withChains(contractSnapshot())
	snap.Nodes[0].Report.Chain.AvgBlockTimeSec = 6
	snap.Nodes[0].Report.Chain.Upgrade = &report.ChainUpgrade{Name: "v0-4-0", Height: 700, Staged: true, Checksums: report.ChecksumsMatch}
	snap.Nodes[1].Report.Chain.AvgBlockTimeSec = 6
	snap.Nodes[1].Report.Chain.Upgrade = &report.ChainUpgrade{Name: "v0-4-0", Height: 700, Staged: true, Checksums: report.ChecksumsMismatch, ChecksumProblem: "the staged verifier is not the one the plan names"}

	var table bytes.Buffer
	if err := StatusTable(snap, nil, &table); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"binary staged on this node and matches the plan",
		"staged binary does NOT match the plan: the staged verifier is not the one the plan names",
	} {
		if !strings.Contains(table.String(), want) {
			t.Errorf("status table misses %q:\n%s", want, table.String())
		}
	}
}
