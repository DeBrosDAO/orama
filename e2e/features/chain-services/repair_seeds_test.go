//go:build e2e_fleet

package chainservices

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// The repair delegate's passes (docs/whitepaper/technical-reference/vol2/41-storage-deals.md "Repair delegate").
const (
	repairRPC      = "http://127.0.0.1:31001"
	repairInterval = "2s"
	// repairRunSeconds covers the first pass and one more; timeout ends the loop.
	repairRunSeconds = "6"
	goodSeedDeal     = "424242"
	badSeedDeal      = "424243"
	// delegateOperator is a placeholder address: no deal names it.
	delegateOperator = "orama1e2erepairdelegateplaceholder"
)

// TestRepairDelegate_oneBadSeedFileDoesNotStopTheOtherDeals: a deal file the
// delegate refuses (readable by others) is logged as an error every pass and
// its deal is not repaired, while the other deals in the directory are still
// visited in the same pass (docs/whitepaper/technical-reference/vol2/41-storage-deals.md "Repair delegate"). Runs the
// binary against a scratch home and the node's own chain RPC, with no deal
// naming the delegate, so nothing is repaired or uploaded.
func TestRepairDelegate_oneBadSeedFileDoesNotStopTheOtherDeals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Nodes()[0]
	if c.F.Exec(t, n, "test -x "+globalBin).Exit != 0 {
		harness.SkipNotApplicable(t, n.Name+" has no "+globalBin+": deploy orama-global to exercise the repair delegate")
	}
	home := strings.TrimSpace(c.F.MustExec(t, n, "mktemp -d /root/e2e-repair-XXXXXX").Stdout)
	t.Cleanup(func() { edge.RunInCleanup(t, c.F, n, "rm -rf -- "+fleet.ShellQuote(home)) })
	seed := strings.Repeat("ab", 32)
	c.F.MustExec(t, n, "mkdir -m 0700 "+fleet.ShellQuote(home+"/deals"))
	c.F.WriteFile(t, n, home+"/operator", []byte(delegateOperator+"\n"), 0o600)
	c.F.WriteFile(t, n, home+"/deals/"+badSeedDeal+".json", []byte(`{"deal_id":`+badSeedDeal+`,"repair_seed":"`+seed+`"}`), 0o644)
	c.F.WriteFile(t, n, home+"/deals/"+goodSeedDeal+".json", []byte(`{"deal_id":`+goodSeedDeal+`,"repair_seed":"`+seed+`"}`), 0o600)

	res := c.F.Exec(t, n, "timeout "+repairRunSeconds+" "+globalBin+" repair --rpc "+repairRPC+
		" --home "+fleet.ShellQuote(home)+" --interval "+repairInterval+" 2>&1")
	out := res.Stdout
	if !strings.Contains(out, badSeedDeal+".json is mode 644") {
		t.Errorf("the loose-mode seed file was not reported:\n%s", c.F.Redact(out))
	}
	if !strings.Contains(out, "deal "+goodSeedDeal+":") {
		t.Errorf("deal %s was not visited after deal %s's file was refused:\n%s", goodSeedDeal, badSeedDeal, c.F.Redact(out))
	}
	if strings.Contains(out, "deal "+badSeedDeal+":") {
		t.Errorf("deal %s was repaired from a refused seed file:\n%s", badSeedDeal, c.F.Redact(out))
	}
}
