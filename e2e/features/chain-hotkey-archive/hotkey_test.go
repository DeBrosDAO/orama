//go:build e2e_fleet

package chainhotkeyarchive

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// TestFundHotKey_earningsBecomeAFeeOnlyBalance: MsgFundHotKey moves the
// operator's own earnings to the fee-only balance of the hot key registered
// on the operator's own node (the hot key proved it holds itself when the
// node registered it). The target is never a field of the message; the
// balance is readable through the FeeBalance query, adds up over several
// funds, is not earnings and not a bank balance (it can pay a base fee and
// nothing else), and the fees invariants hold.
func TestFundHotKey_earningsBecomeAFeeOnlyBalance(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, k)
	node := c.RegisterProvenNode(t, k, []string{chain.RoleRelay}, "relay")
	n := c.Node(t, chain.OperatorNode)
	if got := feeBalance(t, c, n, node.HotKey); !got.IsZero() {
		t.Fatalf("the fee balance of a hot key nobody funded is %s", got.String())
	}
	r := chain.RequireOK(t, "fund the hot key", c.Submit(t, k, chain.TxOptions{}, fundHotKeyMsg(k.Address, node.ID, fmt.Sprint(fundAmount))))
	for key, want := range map[string]string{"operator": k.Address, "node_id": node.ID, "hot_key": node.HotKey, "amount": fmt.Sprint(fundAmount)} {
		if got, ok := chain.Attr(r.Events, "fund_hot_key", key); !ok || got != want {
			t.Errorf("fund_hot_key event %s is %q (present %v), want %q", key, got, ok, want)
		}
	}
	if got := feeBalance(t, c, n, node.HotKey); got.Cmp(chain.NewInt(fundAmount)) != 0 {
		t.Errorf("the hot key's fee balance is %s after one fund, want %d", got.String(), fundAmount)
	}
	chain.RequireOK(t, "fund the hot key again", c.Submit(t, k, chain.TxOptions{}, fundHotKeyMsg(k.Address, node.ID, fmt.Sprint(fundAmount))))
	for _, v := range c.Nodes() {
		if got := feeBalance(t, c, v, node.HotKey); got.Cmp(chain.NewInt(2*fundAmount)) != 0 {
			t.Errorf("%s: the hot key's fee balance is %s after two funds, want %d", v.Name, got.String(), 2*fundAmount)
		}
	}
	if bank := c.Bank(t, n, node.HotKey); !bank.IsZero() {
		t.Errorf("the hot key holds %s norama in the bank: a fee balance is not a bank balance", bank.String())
	}
	if earn := c.Earnings(t, n, node.HotKey); !earn.IsZero() {
		t.Errorf("the hot key holds %s norama of earnings: a fee balance can never be bonded, shielded or deposited", earn.String())
	}
	c.RequireInvariants(t, "a hot key funded from earnings")
}

// TestFundHotKey_refusals: only the node's operator funds its hot key, the
// amount is a positive integer the operator's earnings cover, the node
// exists and is not retired; every refusal leaves the fee balance as it was.
func TestFundHotKey_refusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	other := c.FundedValidator(t, 1, chain.Orama(1))
	c.EnsureOperator(t, k)
	node := c.RegisterProvenNode(t, k, []string{chain.RoleRelay}, "relay")
	n := c.Node(t, chain.OperatorNode)
	chain.RequireRefused(t, "another account funding the hot key",
		c.Submit(t, other, chain.TxOptions{}, fundHotKeyMsg(other.Address, node.ID, fmt.Sprint(fundAmount))), "signer is not the operator")
	for name, amount := range map[string]string{"a zero amount": "0", "a negative amount": "-1"} {
		chain.RequireRefused(t, name, c.Submit(t, k, chain.TxOptions{}, fundHotKeyMsg(k.Address, node.ID, amount)), "amount must be a positive integer")
	}
	chain.RequireRefused(t, "more than the earnings", c.Submit(t, k, chain.TxOptions{},
		fundHotKeyMsg(k.Address, node.ID, "1000000000000000000000000000000")), "insufficient earnings")
	chain.RequireRefused(t, "an unknown node", c.Submit(t, k, chain.TxOptions{},
		fundHotKeyMsg(k.Address, chain.UniqueID(t, "e2e-none-"), fmt.Sprint(fundAmount))), "not found")
	if got := feeBalance(t, c, n, node.HotKey); !got.IsZero() {
		t.Fatalf("a refused fund left %s norama on the hot key", got.String())
	}
	chain.RequireOK(t, "retire the node", c.Submit(t, k, chain.TxOptions{}, chain.RetireNodeMsg(k.Address, node.ID)))
	chain.RequireRefused(t, "a retired node", c.Submit(t, k, chain.TxOptions{},
		fundHotKeyMsg(k.Address, node.ID, fmt.Sprint(fundAmount))), "NODE_STATUS_RETIRED")
	c.RequireInvariants(t, "refused hot key funds")
}

// TestFeeBalance_queryAnswers: FeeBalance is zero for an account nobody
// funded (a validator operator, a module address), requires an address, and
// refuses one that is not bech32; every validator answers alike.
func TestFeeBalance_queryAnswers(t *testing.T) {
	t.Parallel()
	chain.RequireFreshChain(t)
	c := chain.New(t)
	for _, addr := range []string{c.Validator(t, c.Node(t, 0)).Address, chain.ModuleAddress("fee_collector")} {
		for _, n := range c.Nodes() {
			if got := feeBalance(t, c, n, addr); !got.IsZero() {
				t.Errorf("%s: the fee balance of %s is %s, want 0", n.Name, addr, got.String())
			}
		}
	}
	for name, req := range map[string]chain.PB{"no address": {}, "a malformed address": chain.PB{}.Text(1, "not-an-address")} {
		a := c.ABCIQuery(t, c.Node(t, 0), feesQuery+"FeeBalance", req)
		if a.Code == 0 {
			t.Errorf("FeeBalance with %s succeeded: %x", name, a.Value)
		}
	}
}
