//go:build e2e_fleet

package chaincoredestructive

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// exportedModules are the custom modules whose state must be in an export
// (chain/app/app.go wiring; docs/CHAIN.md "Modules wired").
var exportedModules = []string{"emission", "power", "fees", "token", "archive", "nodes", "houses", "storage", "relay", "cnft", "market"}

// exportSummary is what the node-side summariser prints about an export.
type exportSummary struct {
	ChainID       string   `json:"chain_id"`
	InitialHeight string   `json:"initial_height"`
	Modules       []string `json:"modules"`
	Validators    int      `json:"validators"`
	Committee     int      `json:"committee"`
	GenesisSupply string   `json:"genesis_supply"`
	Minted        string   `json:"cumulative_minted"`
}

// summarise prints the export's shape without copying megabytes of state
// back to the runner (and without any key: an export holds none).
const summarise = `python3 - "$D/export.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
a = d.get("app_state", {})
e = a.get("emission", {}).get("epoch_state", {})
print(json.dumps({"chain_id": d.get("chain_id"), "initial_height": str(d.get("initial_height")),
  "modules": sorted(a.keys()), "validators": len(d.get("consensus", {}).get("validators") or d.get("validators") or []),
  "committee": len(a.get("power", {}).get("bootstrap_committee") or []),
  "genesis_supply": e.get("genesis_supply"), "cumulative_minted": e.get("cumulative_minted")}))
PY`

// TestChainExport_stoppedNodeExportsValidGenesis: `oramad export` on a
// stopped validator produces a genesis for the same chain that carries every
// custom module's state, the validator set and x/emission's accounting, and
// `oramad genesis validate` accepts it (the hard-fork path docs/CHAIN.md
// describes; chain/app TestApp_exportImportRoundTrip is its unit twin). The
// node is started again and must catch up.
func TestChainExport_stoppedNodeExportsValidGenesis(t *testing.T) {
	c := chain.New(t)
	victim := c.Node(t, len(c.Nodes())-1)
	live := c.Epoch(t, victim, 0)
	chainAdvancesAtCleanup(t, c)
	c.F.StopService(t, victim, chain.Unit)
	dir := "/tmp/e2e-chainexport"
	t.Cleanup(func() { c.CleanupExec(t, victim, "rm -rf -- "+dir) })
	prep := c.F.Exec(t, victim, fmt.Sprintf("rm -rf -- %s && install -d -o %s -g %s -m 0700 %s", dir, chain.ServiceUser, chain.ServiceUser, dir))
	if prep.Exit != 0 {
		t.Fatalf("failed to create %s: %s", dir, prep.Stderr)
	}
	export := c.OramadCmd("export", "--output-document", dir+"/export.json")
	out := c.Run(t, victim, chain.TxBudget, export)
	if out.Exit != 0 {
		t.Fatalf("oramad export exited %d: %s", out.Exit, c.F.Redact(out.Stderr))
	}
	validate := c.Run(t, victim, chain.QueryBudget, c.OramadCmd("genesis", "validate", dir+"/export.json"))
	if validate.Exit != 0 {
		t.Errorf("oramad genesis validate refused the export: %s %s", validate.Stdout, validate.Stderr)
	}
	sum := summariseExport(t, c, victim, dir)
	if sum.ChainID != c.ID {
		t.Errorf("export names chain %q, want %q", sum.ChainID, c.ID)
	}
	have := map[string]bool{}
	for _, m := range sum.Modules {
		have[m] = true
	}
	for _, m := range exportedModules {
		if !have[m] {
			t.Errorf("export has no %s state (modules: %v)", m, sum.Modules)
		}
	}
	if sum.GenesisSupply != live.GenesisSupply.String() {
		t.Errorf("export genesis_supply %s, live %s", sum.GenesisSupply, live.GenesisSupply.String())
	}
	if sum.Committee != len(c.Nodes()) {
		t.Errorf("export has %d bootstrap committee members, want %d", sum.Committee, len(c.Nodes()))
	}
	if sum.Validators != len(c.Nodes()) {
		t.Errorf("export has %d validators, want %d", sum.Validators, len(c.Nodes()))
	}
	c.F.MustExec(t, victim, "systemctl start "+chain.Unit)
	requireCaughtUp(t, c, victim)
}

func summariseExport(t *testing.T, c *chain.Chain, n fleet.Node, dir string) exportSummary {
	t.Helper()
	out := c.Run(t, n, chain.QueryBudget, "D="+fleet.ShellQuote(dir)+"\n"+summarise)
	if out.Exit != 0 {
		t.Fatalf("failed to read the export: %s", out.Stderr)
	}
	var s exportSummary
	if err := json.Unmarshal([]byte(out.Stdout), &s); err != nil {
		t.Fatalf("export summary: %v: %s", err, out.Stdout)
	}
	return s
}
