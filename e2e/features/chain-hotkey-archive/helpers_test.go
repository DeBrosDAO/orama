//go:build e2e_fleet

package chainhotkeyarchive

import (
	"encoding/base64"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	feesQuery    = "/orama.fees.v1.Query/"
	storageQuery = "/orama.storage.v1.Query/"
)

// fundAmount is what the tests move to a hot key: 0.001 ORAMA in norama.
const fundAmount = 1_000_000

func fundHotKeyMsg(operator, nodeID, amount string) chain.Msg {
	return chain.NewMsg("/orama.nodes.v1.MsgFundHotKey", map[string]any{"operator": operator, "node_id": nodeID, "amount": amount})
}

// fundHotKeyFromBankMsg is MsgFundHotKey with source FUND_SOURCE_BANK: the amount comes from the
// operator's bank balance and not from its earnings.
func fundHotKeyFromBankMsg(operator, nodeID, amount string) chain.Msg {
	return chain.NewMsg("/orama.nodes.v1.MsgFundHotKey", map[string]any{
		"operator": operator, "node_id": nodeID, "amount": amount, "source": "FUND_SOURCE_BANK",
	})
}

// feeBalance is orama.fees.v1.Query/FeeBalance of addr on node n.
func feeBalance(t *testing.T, c *chain.Chain, n fleet.Node, addr string) chain.Int {
	t.Helper()
	a := c.ABCIQuery(t, n, feesQuery+"FeeBalance", chain.PB{}.Text(1, addr))
	if a.Code != 0 {
		t.Fatalf("FeeBalance(%s) on %s: code %d: %s", addr, n.Name, a.Code, a.Log)
	}
	f, err := chain.DecodePB(a.Value)
	if err != nil {
		t.Fatalf("FeeBalance(%s): %v", addr, err)
	}
	var bal chain.Int
	if s := f.Str(1); s != "" {
		if _, ok := bal.SetString(s, 10); !ok {
			t.Fatalf("FeeBalance(%s) is %q, not an integer", addr, s)
		}
	}
	return bal
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
