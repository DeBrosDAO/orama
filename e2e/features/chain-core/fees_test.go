//go:build e2e_fleet

package chaincore

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// Codespace and codes of the SDK errors the ante chain returns
// (cosmos-sdk types/errors).
const (
	sdkSpace            = "sdk"
	codeUnauthorized    = 4
	codeInsufficientFun = 5
	codeUnknownAddress  = 9
	codeOutOfGas        = 11
	codeMemoTooLarge    = 12
	codeInsufficientFee = 13
	codeTxTimeoutHeight = 30
	codeWrongSequence   = 32
	codeInvalidGasLimit = 41
	codeInvalidPubKey   = 8
	codeSigVerifyFailed = 4
	// codeTxInMempoolCache is sdkerrors.ErrTxInMempoolCache.
	codeTxInMempoolCache = 19
)

// feesParams is orama.fees.v1.Params.
type feesParams struct {
	Params struct {
		TargetBlockGasFraction   chain.Dec `json:"target_block_gas_fraction"`
		MaxBaseFeeChangeFraction chain.Dec `json:"max_base_fee_change_fraction"`
		MinBaseFee               chain.Int `json:"min_base_fee"`
		InitialBaseFee           chain.Int `json:"initial_base_fee"`
		DepositRefundFraction    chain.Dec `json:"deposit_refund_fraction"`
		DepositBurnFraction      chain.Dec `json:"deposit_burn_fraction"`
	} `json:"params"`
}

// TestFees_paramsAreTheGenesisDefaults: the fee market runs with the
// documented genesis defaults (docs/whitepaper/technical-reference/vol2/40-economics.md "x/fees": 50% target, 12.5% max
// move, floor 1 norama/gas; deposits 99% refund / 1% burn,
// x/fees/types/params.go DefaultParams), and the base fee on a chain whose
// blocks are nowhere near half of max_gas sits at the floor, as an integer.
func TestFees_paramsAreTheGenesisDefaults(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	var p feesParams
	c.Query(t, n, &p, "fees", "params")
	checks := map[string][2]float64{
		"target_block_gas_fraction":    {p.Params.TargetBlockGasFraction.Float(), 0.5},
		"max_base_fee_change_fraction": {p.Params.MaxBaseFeeChangeFraction.Float(), 0.125},
		"deposit_refund_fraction":      {p.Params.DepositRefundFraction.Float(), 0.99},
		"deposit_burn_fraction":        {p.Params.DepositBurnFraction.Float(), 0.01},
	}
	for name, got := range checks {
		if got[0] != got[1] {
			t.Errorf("%s = %v, want %v", name, got[0], got[1])
		}
	}
	if p.Params.MinBaseFee.Cmp(chain.NewInt(1)) != 0 || p.Params.InitialBaseFee.Cmp(chain.NewInt(1)) != 0 {
		t.Errorf("min/initial base fee %s/%s, want 1/1 norama per gas", p.Params.MinBaseFee.String(), p.Params.InitialBaseFee.String())
	}
	for _, v := range c.Nodes() {
		if bf := c.BaseFee(t, v); bf.Cmp(p.Params.MinBaseFee) != 0 {
			t.Errorf("%s: base fee %s on an idle chain, want the floor %s", v.Name, bf.String(), p.Params.MinBaseFee.String())
		}
	}
}

// TestFees_belowBaseFeeRefused: a fee one norama under base_fee*gas is
// refused by CheckTx with ErrInsufficientFee (x/fees/ante/fee_decorator.go),
// and never reaches a block.
func TestFees_belowBaseFeeRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	r := c.Submit(t, k, chain.TxOptions{FeeDelta: -1}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "fee under the base fee", r, sdkSpace, codeInsufficientFee, "want at least the base fee")
	if r.Stage != chain.StageCheck {
		t.Errorf("under-fee transaction ended at %s, want CheckTx", r.Stage)
	}
}

// TestFees_zeroFeeRefused: no fee at all fails the validator's local
// minimum-gas-prices policy first (docs/whitepaper/technical-reference/vol2/40-economics.md "Other genesis defaults":
// 0.000001norama; fee_decorator.go checkValidatorMinGasPrice).
func TestFees_zeroFeeRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	r := c.Submit(t, k, chain.TxOptions{Mode: chain.FeeAbsolute, FeeAmount: "0"}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "zero fee", r, sdkSpace, codeInsufficientFee, "insufficient fees")
}

// TestFees_tipMustComeFromBank: anything above base_fee*gas is a tip, and a
// tip is paid from the public bank balance only, never from earnings
// (x/fees/keeper/feepay.go SettleFee, security review M4). A validator's
// payouts are all earnings, so a tip larger than its bank balance is refused
// even though its earnings could cover it many times over.
func TestFees_tipMustComeFromBank(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	bank := c.Bank(t, k.Node, k.Address)
	if !bank.IsInt64() {
		t.Fatalf("bank balance %s does not fit a tip delta", bank.String())
	}
	r := c.Submit(t, k, chain.TxOptions{FeeDelta: bank.Int64() + 1}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "tip above the bank balance", r, sdkSpace, codeInsufficientFun,
		"a tip must come from a public bank balance, never earnings")
}

// TestFees_baseFeeBurnedFromEarnings: a successful transaction's fee is
// exactly base_fee(previous block) * gas_limit, all of it base fee (tip 0),
// paid from the signer's earnings when the bank balance is empty, and burned:
// the fee counters move by it with collected == burned + distributed
// (docs/whitepaper/technical-reference/vol2/40-economics.md "The fee ante decorator").
func TestFees_baseFeeBurnedFromEarnings(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	r, h := submitOutsideEpochClose(t, c, k)
	fee := eventInt(t, r, "tx", "base_fee")
	if tip := eventInt(t, r, "tx", "tip"); !tip.IsZero() {
		t.Errorf("tip %s, want 0", tip.String())
	}
	bf := c.BaseFeeAt(t, k.Node, h-1)
	want := chain.NewInt(bf.Int64() * r.GasWanted)
	if fee.Cmp(want) != 0 {
		t.Errorf("base fee paid %s, want base fee %s x gas %d = %s", fee.String(), bf.String(), r.GasWanted, want.String())
	}
	before, after := c.EarningsAt(t, k.Node, k.Address, h-1), c.EarningsAt(t, k.Node, k.Address, h)
	bankBefore, bankAfter := c.BankAt(t, k.Node, k.Address, h-1), c.BankAt(t, k.Node, k.Address, h)
	if bankBefore.IsZero() && before.Sub(after).Cmp(fee) != 0 {
		t.Errorf("earnings went %s -> %s, want down by exactly the fee %s", before.String(), after.String(), fee.String())
	}
	if bankBefore.Cmp(bankAfter) < 0 {
		t.Errorf("bank balance grew %s -> %s while paying a fee", bankBefore.String(), bankAfter.String())
	}
	f0, f1 := c.Fees(t, k.Node, h-1), c.Fees(t, k.Node, h)
	burned := f1.Burned.Sub(f0.Burned)
	if burned.Cmp(fee) < 0 {
		t.Errorf("cumulative burned grew by %s in block %d, less than this tx's base fee %s", burned.String(), h, fee.String())
	}
	collected, distributed := f1.Collected.Sub(f0.Collected), f1.Distributed.Sub(f0.Distributed)
	if collected.Cmp(burned.Add(distributed)) != 0 {
		t.Errorf("block %d: collected %s != burned %s + distributed %s", h, collected.String(), burned.String(), distributed.String())
	}
	c.RequireInvariants(t, "a fee paid from earnings")
}

// epochCloseAttempts bounds how often a measurement is repeated because an
// epoch close (which credits earnings in the same block) landed in its block.
const epochCloseAttempts = 3

// submitOutsideEpochClose submits a harmless transaction until one lands in a
// block where no epoch closed, so the signer's earnings change only by the fee.
func submitOutsideEpochClose(t *testing.T, c *chain.Chain, k chain.Key) (chain.Result, int64) {
	t.Helper()
	for i := 0; i < epochCloseAttempts; i++ {
		r := chain.RequireOK(t, "harmless transaction", c.Submit(t, k, chain.TxOptions{}, harmlessMsg(t, c, k)))
		if c.Epoch(t, k.Node, r.Height-1).CurrentEpoch.Cmp(c.Epoch(t, k.Node, r.Height).CurrentEpoch) == 0 {
			return r, r.Height
		}
	}
	t.Fatalf("an epoch closed in the block of each of %d transactions; the epoch length is too short to measure a fee", epochCloseAttempts)
	return chain.Result{}, 0
}

func eventInt(t *testing.T, r chain.Result, typ, key string) chain.Int {
	t.Helper()
	v, ok := chain.Attr(r.Events, typ, key)
	if !ok {
		t.Fatalf("transaction %s has no %s.%s event attribute", r.TxHash, typ, key)
	}
	var i chain.Int
	if err := i.UnmarshalJSON([]byte(fmt.Sprintf("%q", v))); err != nil {
		t.Fatalf("%s.%s = %q: %v", typ, key, v, err)
	}
	return i
}
