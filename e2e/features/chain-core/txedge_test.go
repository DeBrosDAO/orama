//go:build e2e_fleet

package chaincore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// blockMaxGas is the run chain's consensus block max_gas
// (e2e/scripts/chain-deploy.sh build_genesis, docs/whitepaper/technical-reference/vol2/39-chain-architecture.md).
const blockMaxGas = 100_000_000

// maxMemoChars is x/auth's default MaxMemoCharacters (the app keeps it).
const maxMemoChars = 256

// wrongAccountNumberDelta moves the signed account number far from the
// signer's own.
const wrongAccountNumberDelta = 1000

// TestTx_replayRefused: broadcasting a delivered transaction again to the
// same node is refused before CheckTx runs: CometBFT's mempool cache still
// holds it (mempool/clist_mempool.go ErrTxInCache), which the SDK client
// reports as sdk/19 ErrTxInMempoolCache with exit 0 (client/broadcast.go
// CheckCometError). Under k's lock, k's sequence does not move.
func TestTx_replayRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	first := chain.RequireOK(t, "original", c.Submit(t, k, chain.TxOptions{}, harmlessMsg(t, c, k)))
	again, before, after := c.ReplayLocked(t, k, first.Signed)
	if again.Stage != chain.StageCheck || again.Codespace != sdkSpace || again.Code != codeTxInMempoolCache {
		t.Fatalf("want the replay refused as %s/%d at CheckTx, got %s", sdkSpace, codeTxInMempoolCache, again)
	}
	if after != before {
		t.Fatalf("the replay moved %s's sequence from %d to %d", k.Address, before, after)
	}
}

// TestTx_spentSequenceRefused: a NEW body (its own memo, so it is no cached
// replay) signed for a sequence the account already spent is refused by
// CheckTx.
func TestTx_spentSequenceRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	chain.RequireOK(t, "spend a sequence", c.Submit(t, k, chain.TxOptions{}, harmlessMsg(t, c, k)))
	acc, ok := c.AccountOf(t, k.Node, k.Address)
	if !ok || acc.Sequence == 0 {
		t.Fatalf("validator %s has no spent sequence (account found %v, sequence %d)", k.Address, ok, acc.Sequence)
	}
	opts := chain.TxOptions{Offline: true, AccountNumber: acc.Number, Sequence: acc.Sequence - 1, Memo: chain.UniqueID(t, "spent-")}
	r := c.Broadcast(t, k.Node, c.Sign(t, k, opts, harmlessMsg(t, c, k)))
	chain.RequireCode(t, "spent sequence", r, sdkSpace, codeWrongSequence, "account sequence mismatch")
}

// TestTx_badSequenceRefused: a transaction signed for a sequence ahead of (or
// behind) the account's is refused by CheckTx. Each body carries a unique
// memo, so no earlier byte-identical transaction answers from the mempool
// cache instead.
func TestTx_badSequenceRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	acc, ok := c.AccountOf(t, k.Node, k.Address)
	if !ok {
		t.Fatalf("validator %s has no account", k.Address)
	}
	for _, seq := range []uint64{acc.Sequence + 5, 0} {
		if seq == acc.Sequence {
			continue
		}
		opts := chain.TxOptions{Offline: true, AccountNumber: acc.Number, Sequence: seq, Memo: chain.UniqueID(t, "badseq-")}
		r := c.Broadcast(t, k.Node, c.Sign(t, k, opts, harmlessMsg(t, c, k)))
		chain.RequireCode(t, "sequence out of order", r, sdkSpace, codeWrongSequence, "account sequence mismatch")
	}
}

// TestTx_wrongAccountNumberRefused: the signature commits to the account
// number; another one fails signature verification. It is signed for the
// live sequence and broadcast under k's lock, so no sequence race can turn
// the refusal into a sequence mismatch.
func TestTx_wrongAccountNumberRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	r := c.SignAndBroadcastLocked(t, k, chain.TxOptions{}, chain.LockedEdit{AccountNumberDelta: wrongAccountNumberDelta}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "wrong account number", r, sdkSpace, codeSigVerifyFailed, "signature verification failed")
}

// TestTx_wrongChainIDRefused: a signature over another chain id (still a
// devnet id: nothing here signs for a real network) fails verification.
func TestTx_wrongChainIDRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	r := c.SignAndBroadcastLocked(t, k, chain.TxOptions{ChainID: c.ID + "-other"}, chain.LockedEdit{}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "wrong chain id", r, sdkSpace, codeSigVerifyFailed, "signature verification failed")
}

// TestTx_wrongSignerRefused: a body whose signer is validator B, carrying
// validator A's signer info and signature, is refused: A's public key does
// not match B's address (x/auth/ante SetPubKeyDecorator), so nobody can act
// for another account by reusing their own signature.
func TestTx_wrongSignerRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	a := c.FundedValidator(t, 1, chain.Orama(1))
	b := c.Validator(t, c.Node(t, 2))
	signed := c.Sign(t, a, chain.TxOptions{}, harmlessMsg(t, c, a))
	forged := retarget(t, signed, c.Valoper(t, a), c.Valoper(t, b))
	r := c.Broadcast(t, a.Node, forged)
	chain.RequireCode(t, "body re-pointed at another validator", r, sdkSpace, codeInvalidPubKey, "pubKey does not match signer address")
}

// TestTx_tamperedBodyRefused: changing the memo of a signed body breaks the
// signature (signed and broadcast under k's lock, like the account number
// test).
func TestTx_tamperedBodyRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	r := c.SignAndBroadcastLocked(t, k, chain.TxOptions{Memo: "e2e original"}, chain.LockedEdit{Memo: "e2e tampered"}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "tampered memo", r, sdkSpace, codeSigVerifyFailed, "signature verification failed")
}

// TestTx_gasLimits: gas far too low runs out in the ante chain (the tx-size
// charge comes first); gas one above the block's max_gas is refused outright;
// a gas limit of 1 is out of gas too (boundary values). A zero gas limit is
// not expressible here: TxOptions treats 0 as the default.
func TestTx_gasLimits(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	low := c.Submit(t, k, chain.TxOptions{Gas: 1000}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "gas 1000", low, sdkSpace, codeOutOfGas, "out of gas")
	over := c.Submit(t, k, chain.TxOptions{Gas: blockMaxGas + 1}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "gas above block max", over, sdkSpace, codeInvalidGasLimit, "exceeds block max gas")
	one := c.Submit(t, k, chain.TxOptions{Gas: 1, Mode: chain.FeeAbsolute, FeeAmount: "1"}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "gas 1", one, sdkSpace, codeOutOfGas, "out of gas")
}

// TestTx_timeoutHeightPassedRefused: a transaction whose timeout height is
// already behind the chain is refused.
func TestTx_timeoutHeightPassedRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	past := uint64(c.Height(t) - 1)
	r := c.Submit(t, k, chain.TxOptions{TimeoutHeight: past}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "timeout height in the past", r, sdkSpace, codeTxTimeoutHeight, "timeout")
}

// TestTx_memoTooLongRefused: a memo one character over the limit is refused,
// one at the limit is accepted (boundary).
func TestTx_memoTooLongRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	over := c.Submit(t, k, chain.TxOptions{Memo: strings.Repeat("m", maxMemoChars+1)}, harmlessMsg(t, c, k))
	chain.RequireCode(t, "memo over the limit", over, sdkSpace, codeMemoTooLarge, "maximum number of characters")
	chain.RequireOK(t, "memo at the limit", c.Submit(t, k, chain.TxOptions{Memo: strings.Repeat("m", maxMemoChars)}, harmlessMsg(t, c, k)))
}

// TestTx_unfundedAccountRefused: an account that never received anything
// cannot even pay a fee: the fee payer does not exist (x/fees/ante
// resolvePayer), whatever the message.
func TestTx_unfundedAccountRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 1)
	fresh := c.NewKey(t, n, "e2e-unfunded")
	if _, ok := c.AccountOf(t, n, fresh.Address); ok {
		t.Fatalf("a brand-new key %s already has an account", fresh.Address)
	}
	// Signed offline: the client cannot look up an account that does not exist.
	r := c.Submit(t, fresh, chain.TxOptions{Offline: true}, chain.NewMsg("/orama.nodes.v1.MsgRegisterOperator", map[string]any{"operator": fresh.Address}))
	chain.RequireCode(t, "unfunded signer", r, sdkSpace, codeUnknownAddress, "does not exist")
}

// retarget replaces every occurrence of from with to in a signed
// transaction's messages, leaving signer infos and signatures as they were.
func retarget(t *testing.T, signed []byte, from, to string) []byte {
	t.Helper()
	var tx map[string]any
	if err := json.Unmarshal(signed, &tx); err != nil {
		t.Fatal(err)
	}
	body := tx["body"].(map[string]any)
	raw, err := json.Marshal(body["messages"])
	if err != nil {
		t.Fatal(err)
	}
	var msgs any
	if err := json.Unmarshal([]byte(strings.ReplaceAll(string(raw), from, to)), &msgs); err != nil {
		t.Fatal(err)
	}
	body["messages"] = msgs
	out, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
