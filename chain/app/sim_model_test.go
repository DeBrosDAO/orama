package app_test

import (
	"fmt"
	"sort"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// simToken is the simulation's own account of one x/token denom: who holds what, what the creator may
// still do, and the rules a transfer, mint or burn is subject to. It is written from the documented
// rules (website/src/docs/blockchain/token-factory.mdx), not by calling x/token, so a disagreement
// between it and the chain is a finding in one of the two.
type simToken struct {
	denom           string
	creator         sdk.AccAddress
	canMint         bool
	canFreeze       bool
	canPause        bool
	feeBps          int64
	nonTransferable bool
	paused          bool
	frozen          map[string]bool
	balances        map[string]math.Int
	issued          math.Int
}

func newSimToken(denom string, creator sdk.AccAddress, mint, freeze, pause bool, feeBps int64, nonTransferable bool) *simToken {
	return &simToken{
		denom: denom, creator: creator, canMint: mint, canFreeze: freeze, canPause: pause,
		feeBps: feeBps, nonTransferable: nonTransferable,
		frozen: map[string]bool{}, balances: map[string]math.Int{}, issued: math.ZeroInt(),
	}
}

func (t *simToken) balance(a sdk.AccAddress) math.Int {
	if v, ok := t.balances[a.String()]; ok {
		return v
	}
	return math.ZeroInt()
}

func (t *simToken) credit(a sdk.AccAddress, n math.Int) { t.balances[a.String()] = t.balance(a).Add(n) }
func (t *simToken) debit(a sdk.AccAddress, n math.Int)  { t.balances[a.String()] = t.balance(a).Sub(n) }

// transferFee is floor(amount * bps / 10,000): the fee a MsgTransfer burns.
func (t *simToken) transferFee(amount math.Int) math.Int {
	return amount.MulRaw(t.feeBps).QuoRaw(10_000)
}

// canMove is whether a plain move of amount from -> to is allowed at all by the token's flags and
// the freeze list. MsgTransfer and a bank MsgSend share these.
func (t *simToken) canMove(from, to sdk.AccAddress, amount math.Int) bool {
	return amount.IsPositive() && !from.Equals(to) && !t.paused && !t.nonTransferable &&
		!t.frozen[from.String()] && !t.frozen[to.String()] && t.balance(from).GTE(amount)
}

// predictMint reports whether the model says a mint by sender succeeds, and applies it if so.
func (t *simToken) predictMint(sender, to sdk.AccAddress, amount math.Int) bool {
	if !amount.IsPositive() || !t.canMint || !t.creator.Equals(sender) || t.frozen[to.String()] {
		return false
	}
	t.credit(to, amount)
	t.issued = t.issued.Add(amount)
	return true
}

// predictBurn reports whether a burn by holder succeeds, and applies it if so.
func (t *simToken) predictBurn(holder sdk.AccAddress, amount math.Int) bool {
	if !amount.IsPositive() || t.frozen[holder.String()] || t.balance(holder).LT(amount) || t.issued.LT(amount) {
		return false
	}
	t.debit(holder, amount)
	t.issued = t.issued.Sub(amount)
	return true
}

// predictTransfer reports whether MsgTransfer from -> to succeeds, and applies it if so: the
// recipient gets amount minus the fee, and the fee is burned.
func (t *simToken) predictTransfer(from, to sdk.AccAddress, amount math.Int) bool {
	if !t.canMove(from, to, amount) {
		return false
	}
	fee := t.transferFee(amount)
	t.debit(from, amount)
	t.credit(to, amount.Sub(fee))
	t.issued = t.issued.Sub(fee)
	return true
}

// predictPlainSend reports whether a bank MsgSend of the denom succeeds. A token with a fee, a pause
// flag or a non-transferable flag moves only through MsgTransfer.
func (t *simToken) predictPlainSend(from, to sdk.AccAddress, amount math.Int) bool {
	if t.feeBps > 0 || !t.canMove(from, to, amount) {
		return false
	}
	t.debit(from, amount)
	t.credit(to, amount)
	return true
}

// predictSetFrozen reports whether the creator's freeze or thaw succeeds, and applies it if so.
func (t *simToken) predictSetFrozen(sender, account sdk.AccAddress, frozen bool) bool {
	if !t.canFreeze || !t.creator.Equals(sender) {
		return false
	}
	if frozen {
		t.frozen[account.String()] = true
	} else {
		delete(t.frozen, account.String())
	}
	return true
}

// predictSetPaused reports whether the creator's pause or unpause succeeds, and applies it if so.
func (t *simToken) predictSetPaused(sender sdk.AccAddress, paused bool) bool {
	if !t.canPause || !t.creator.Equals(sender) {
		return false
	}
	t.paused = paused
	return true
}

// sortedDenoms returns the model's denoms in a fixed order.
func simDenoms(tokens map[string]*simToken) []string {
	out := make([]string, 0, len(tokens))
	for d := range tokens {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func describeSimTx(name string, signer int, extra string) string {
	return fmt.Sprintf("%s by user %d %s", name, signer, extra)
}
