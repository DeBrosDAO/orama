//go:build e2e_fleet

package chaincli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
)

// badAddresses are arguments that are not orama addresses, or would change
// the path a URL is built from: refused as usage before anything is read.
var badAddresses = []string{"not-an-address", "orama1", "cosmos1qyqszqgpqyqszqgpqyqszqgpqyqszqgpjnp7du", "orama1abcdefgh/../x", "orama1abcdefgh?x=1"}

// TestChainBalance_printsTheBankBalance: `orama chain balance` prints an
// account's spendable bank balance from --node's REST API (no run account holds
// one: earnings are a separate ledger), the same amounts as the node's own
// bank query, as text and as JSON; it needs --node, and refuses arguments
// that are not orama addresses.
func TestChainBalance_printsTheBankBalance(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	rest := restURL(t, c)
	k := c.Validator(t, c.Node(t, readerNode))
	want := "no balance"
	if bank := c.Bank(t, k.Node, k.Address); !bank.IsZero() {
		want = fmt.Sprintf("%s %s", bank.String(), chain.Denom)
	}
	res := run(t, "chain", "balance", k.Address, "--node", rest)
	infra.ExpectExit(t, res, infra.ExitOK)
	if got := strings.TrimSpace(res.Stdout); got != want {
		t.Errorf("chain balance printed %q, want %q", got, want)
	}
	var doc struct {
		Balances []struct{ Denom, Amount string } `json:"balances"`
	}
	runJSON(t, &doc, "chain", "balance", k.Address, "--node", rest, "--json")
	if (len(doc.Balances) == 0) != (want == "no balance") {
		t.Errorf("chain balance --json lists %v, text said %q", doc.Balances, want)
	}
	requireRead(t, run(t, "chain", "balance", k.Address), "--node")
	requireRead(t, run(t, "chain", "balance", k.Address, "--node", deadRPC), "read")
	for _, bad := range badAddresses {
		requireUsage(t, run(t, "chain", "balance", bad, "--node", rest), "not an orama address")
	}
}

// earningsOf reads `orama chain earnings <addr>` and returns its balance.
func earningsOf(t *testing.T, addr string, extra ...string) chain.Int {
	t.Helper()
	var doc any
	runJSON(t, &doc, append([]string{"chain", "earnings", addr}, extra...)...)
	return amountOf(t, doc, "balance")
}

// TestChainEarnings_readsX_fees: `orama chain earnings` prints the x/fees
// earnings balance of an account, through --rpc (abci_query) and through the
// gateway: a validator operator that has been paid holds some, an account
// nothing ever paid holds zero, and a malformed address is a usage error.
func TestChainEarnings_readsX_fees(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	rpc := rpcURL(t, c)
	k := c.FundedValidator(t, readerNode, chain.Orama(1))
	stranger := c.NewKey(t, k.Node, "e2e-cli-earnings")
	for name, extra := range map[string][]string{"--rpc": {"--rpc", rpc}, "the gateway": nil} {
		if got := earningsOf(t, k.Address, extra...); got.Cmp(chain.Orama(1)) < 0 {
			t.Errorf("%s: the validator's earnings are %s norama, want at least 1 ORAMA (the fleet's epochs paid it)", name, got.String())
		}
		if got := earningsOf(t, stranger.Address, extra...); !got.IsZero() {
			t.Errorf("%s: an account nothing paid holds %s norama of earnings", name, got.String())
		}
	}
	for _, bad := range badAddresses {
		requireUsage(t, run(t, "chain", "earnings", bad, "--rpc", rpc), "not an orama address")
	}
	requireRead(t, run(t, "chain", "earnings", k.Address, "--rpc", deadRPC), "read")
}
