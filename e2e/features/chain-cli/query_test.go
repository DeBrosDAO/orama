//go:build e2e_fleet

package chaincli

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// protoRoot is the chain's proto tree, from this package's directory.
var protoRoot = filepath.Join("..", "..", "..", "chain", "proto", "orama")

var queryRPC = regexp.MustCompile(`(?m)^\s*rpc (\w+)\(Query\w+\) returns`)

// declaredQueries are the "orama.<module>.v1.Query/<Rpc>" names chain/proto's
// query.proto files declare.
func declaredQueries(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(protoRoot, "*", "v1", "query.proto"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query.proto under %s: %v", protoRoot, err)
	}
	var out []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		module := filepath.Base(filepath.Dir(filepath.Dir(f)))
		for _, m := range queryRPC.FindAllStringSubmatch(string(raw), -1) {
			out = append(out, "orama."+module+".v1.Query/"+m[1])
		}
	}
	sort.Strings(out)
	return out
}

// TestChainQuery_listsEveryQueryTheChainDeclares: `orama chain query --list`
// prints exactly the query methods chain/proto declares (the CLI embeds the
// descriptors of the release it was built from, so a query added to the
// chain and not to the CLI shows here).
func TestChainQuery_listsEveryQueryTheChainDeclares(t *testing.T) {
	t.Parallel()
	chain.New(t)
	res := run(t, "chain", "query", "--list")
	got := strings.Fields(res.Stdout)
	sort.Strings(got)
	want := declaredQueries(t)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("chain query --list prints %d queries, chain/proto declares %d:\n%s\nwant:\n%s",
			len(got), len(want), strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestChainQuery_runsAnyModuleQuery: a module query runs through --rpc
// (abci_query) and through the gateway with the request as proto-named JSON:
// the base fee is at least the fee market's floor, a fee balance nobody funded
// is zero, the shielded tree of a run chain is empty, and the answers carry
// the proto field names.
func TestChainQuery_runsAnyModuleQuery(t *testing.T) {
	t.Parallel()
	chain.RequireFreshChain(t)
	c := chain.New(t)
	rpc := rpcURL(t, c)
	var fee any
	runJSON(t, &fee, "chain", "query", "orama.fees.v1.Query/BaseFee", "--rpc", rpc)
	if got := amountOf(t, fee, "base_fee"); got.Cmp(chain.NewInt(1)) < 0 {
		t.Errorf("the base fee is %s norama per gas, below the floor of 1", got.String())
	}
	k := c.Validator(t, c.Node(t, readerNode))
	request := `{"address":"` + k.Address + `"}`
	for name, extra := range map[string][]string{"--rpc": {"--rpc", rpc}, "the gateway": nil} {
		var bal any
		runJSON(t, &bal, append([]string{"chain", "query", "orama.fees.v1.Query/FeeBalance", request}, extra...)...)
		if got := amountOf(t, bal, "balance"); !got.IsZero() {
			t.Errorf("%s: the fee balance of %s is %s", name, k.Address, got.String())
		}
	}
	var tree any
	runJSON(t, &tree, "chain", "query", "orama.shielded.v1.Query/TreeState", "--rpc", rpc)
	if got := amountOf(t, tree, "tree_size"); !got.IsZero() {
		t.Errorf("the shielded note tree of a run chain holds %s notes, want 0", got.String())
	}
}

// TestChainQuery_refusals: no query named is a usage error; a name that is
// not Service/Method, an unknown service or method, a request that is not
// JSON for the method, and a key the chain does not have are failed reads
// that say why, and nothing is sent for the first four.
func TestChainQuery_refusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	rpc := rpcURL(t, c)
	requireUsage(t, run(t, "chain", "query"), "name a query")
	requireUsage(t, run(t, "chain", "query", "a", "b", "c"))
	requireRead(t, run(t, "chain", "query", "orama-fees", "--rpc", rpc), "is not Service/Method")
	requireRead(t, run(t, "chain", "query", "orama.nope.v1.Query/Params", "--rpc", rpc), "unknown query service")
	requireRead(t, run(t, "chain", "query", "orama.fees.v1.Query/Nope", "--rpc", rpc), "has no query")
	requireRead(t, run(t, "chain", "query", "orama.fees.v1.Query/FeeBalance", "{not json", "--rpc", rpc), "request for")
	requireRead(t, run(t, "chain", "query", "orama.nodes.v1.Query/Node", `{"node_id":"`+chain.UniqueID(t, "e2e-none-")+`"}`, "--rpc", rpc), notFound)
	requireRead(t, run(t, "chain", "query", "orama.fees.v1.Query/BaseFee", "--rpc", deadRPC), "read")
}
