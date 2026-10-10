//go:build e2e_fleet

// Package chainwaivers pins the CURRENT, documented absence of the chain
// surfaces that are built as libraries but not wired into oramad:
// x/inclusion, x/vpnlaunch, x/confidential and x/wasmbindings. Each test is a
// tripwire: when one of them ships a message, a query or genesis state on the
// live chain, the matching test turns red and forces real tests (and the
// removal of its waiver). x/inclusion is wired into BaseApp's proposal and
// vote-extension handlers only, so it still has none of the three; its switch
// is pinned by TestInclusion_voteExtensionsAreTheDeploysSwitch.
package chainwaivers

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// unwired are the module names (proto package segment and genesis key) that
// register no message, query or genesis state today (docs/CHAIN.md:
// x/inclusion works through BaseApp handlers only; "chain/
// x/confidential refuses every attestation report"; x/wasmbindings answers
// NOT_LINKED; x/vpnlaunch is a pure gate).
var unwired = []string{"inclusion", "vpnlaunch", "confidential", "wasmbindings"}

// msgInterface is the interface every sdk.Msg implementation is registered under.
const msgInterface = "cosmos.base.v1beta1.Msg"

// protoRoot is the chain's proto tree, from this package's directory.
var protoRoot = filepath.Join("..", "..", "..", "chain", "proto")

var (
	protoPackage = regexp.MustCompile(`(?m)^package (orama\.[a-z]+\.v1);`)
	protoRPC     = regexp.MustCompile(`rpc \w+\((Msg\w+)\)`)
)

// declaredMsgs are the orama Msg type names the chain's tx.proto files
// declare (the coverage universe's msg: entries).
func declaredMsgs(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(protoRoot, "orama", "*", "v1", "tx.proto"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no tx.proto under %s: %v", protoRoot, err)
	}
	out := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		pkg := protoPackage.FindStringSubmatch(string(raw))
		if pkg == nil {
			t.Fatalf("%s names no orama package", f)
		}
		for _, m := range protoRPC.FindAllStringSubmatch(string(raw), -1) {
			out[pkg[1]+"."+m[1]] = true
		}
	}
	return out
}

// liveMsgs lists every Msg implementation the node's interface registry
// knows (cosmos.base.reflection.v1beta1 ListImplementations, through
// abci_query).
func liveMsgs(t *testing.T, c *chain.Chain) []string {
	t.Helper()
	a := c.ABCIQuery(t, c.Node(t, 0), "/cosmos.base.reflection.v1beta1.ReflectionService/ListImplementations",
		chain.PB{}.Text(1, msgInterface))
	if a.Code != 0 {
		t.Fatalf("ListImplementations(%s): code %d %s", msgInterface, a.Code, a.Log)
	}
	f, err := chain.DecodePB(a.Value)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range f[1] {
		if b, ok := v.([]byte); ok {
			names = append(names, strings.TrimPrefix(string(b), "/"))
		}
	}
	if len(names) == 0 {
		t.Fatalf("the node registers no Msg implementation at all")
	}
	sort.Strings(names)
	return names
}

// TestUnwired_noMessageOfAnUnwiredModule: the live node registers no message
// of x/inclusion, x/vpnlaunch, x/confidential or x/wasmbindings (so no
// inclusion tx, no attestation can be submitted),
// and its orama messages are exactly the ones chain/proto declares: a new
// message anywhere turns this red until it is added to the coverage universe
// and tested.
func TestUnwired_noMessageOfAnUnwiredModule(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	declared := declaredMsgs(t)
	live := map[string]bool{}
	for _, name := range liveMsgs(t, c) {
		for _, m := range unwired {
			if strings.Contains(name, "."+m+".") {
				t.Errorf("the node registers %s: the %s module has shipped a message; write its tests and drop its waiver", name, m)
			}
		}
		if strings.HasPrefix(name, "orama.") {
			live[name] = true
			if !declared[name] {
				t.Errorf("the node registers %s, which no chain/proto tx.proto declares", name)
			}
		}
	}
	for name := range declared {
		if !live[name] {
			t.Errorf("chain/proto declares %s but the node does not register it", name)
		}
	}
}

// Answers of a module the app does not wire (cosmos-sdk baseapp
// handleQueryGRPC / rootmulti Store.Query, both ErrUnknownRequest).
const (
	noQueryRoute = "unknown query path"
	noStore      = "no such store"
)

// queryProbes are the Query methods asked of every unwired module. A wired
// module answers at least one of them or, whatever its methods are called,
// mounts its store, which the store probe reads.
var queryProbes = []string{"Params", "State", "Status", "Config"}

// TestUnwired_noQueryServiceAndNoGenesisState: none of the unwired modules
// has a query route or a mounted KV store on the node, and none has genesis
// state.
func TestUnwired_noQueryServiceAndNoGenesisState(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	for _, m := range unwired {
		for _, method := range queryProbes {
			a := c.ABCIQuery(t, n, "/orama."+m+".v1.Query/"+method, chain.PB{})
			if a.Code == 0 || !strings.Contains(a.Log, noQueryRoute) {
				t.Errorf("x/%s Query/%s is routed on the node (code %d: %s): write its tests and drop its waiver", m, method, a.Code, a.Log)
			}
		}
		a := c.ABCIQuery(t, n, "/store/"+m+"/key", chain.PB{}.Text(1, m))
		if a.Code == 0 || !strings.Contains(a.Log, noStore) {
			t.Errorf("the node mounts a %s store (code %d: %s): the module is wired; write its tests and drop its waiver", m, a.Code, a.Log)
		}
	}
	out := c.Run(t, n, chain.QueryBudget, "sudo -u "+chain.ServiceUser+" python3 -c "+
		`'import json;print(" ".join(json.load(open("`+chain.Home+`/config/genesis.json"))["app_state"].keys()))'`)
	if out.Exit != 0 {
		t.Fatalf("cannot read the genesis modules: %s", out.Stderr)
	}
	mods := " " + strings.TrimSpace(out.Stdout) + " "
	for _, m := range unwired {
		if strings.Contains(mods, " "+m+" ") {
			t.Errorf("genesis carries %s state: the module is wired; write its tests and drop its waiver", m)
		}
	}
}

// voteExtensionsEnableHeight is the abci.vote_extensions_enable_height each
// deploy writes into genesis (docs/CHAIN.md "Inclusion lists (C13)"):
// e2e/scripts/chain-deploy.sh leaves it 0 (off) on a run chain,
// chain/scripts/stagenet/deploy.sh sets 2.
func voteExtensionsEnableHeight(c *chain.Chain) string {
	if c.F.State.IsStagenet() {
		return "2"
	}
	return "0"
}

// TestInclusion_voteExtensionsAreTheDeploysSwitch: x/inclusion is wired, and
// its bytes ride on vote extensions only from the height the deploy's genesis
// names; the chain cannot change it later (every consensus-param authority is
// unreachable), so the live value is the deploy's.
func TestInclusion_voteExtensionsAreTheDeploysSwitch(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	var r map[string]any
	if err := c.Comet(t, c.Node(t, 0), "/consensus_params", &r); err != nil {
		t.Fatal(err)
	}
	h, ok := findString(r, "vote_extensions_enable_height")
	if !ok {
		t.Fatalf("consensus_params carries no vote_extensions_enable_height: %v", r)
	}
	if want := voteExtensionsEnableHeight(c); h != want {
		t.Errorf("vote extensions are enabled from height %s, the deploy sets %s", h, want)
	}
}

func findString(v any, key string) (string, bool) {
	switch x := v.(type) {
	case map[string]any:
		if s, ok := x[key].(string); ok {
			return s, true
		}
		for _, child := range x {
			if s, ok := findString(child, key); ok {
				return s, true
			}
		}
	case []any:
		for _, child := range x {
			if s, ok := findString(child, key); ok {
				return s, true
			}
		}
	}
	return "", false
}
