//go:build e2e_fleet

package chaincore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// blockMaxGas is the run chain's consensus block max_gas
// (e2e/scripts/chain-deploy.sh build_genesis, docs/CHAIN.md).
const blockMaxGas = 100_000_000

// maxMemoChars is x/auth's default MaxMemoCharacters (the app keeps it).
const maxMemoChars = 256

// TestTx_replayRefused: broadcasting a delivered transaction again is refused
// (its sequence is spent), and state does not change a second time.
func TestTx_replayRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	first := chain.RequireOK(t, "original", c.Submit(t, k, chain.TxOptions{}, harmlessMsg(t, c, k)))
	again := c.Broadcast(t, k.Node, first.Signed)
	if again.OK() {
		t.Fatalf("the replay was delivered: %s", again)
	}
	if again.Stage == chain.StageRPC {
		if !strings.Contains(again.Log, "already exists") {
			t.Fatalf("the RPC refused the replay for another reason: %s", again.Log)
		}
		return
	}
	chain.RequireCode(t, "replay", again, sdkSpace, codeWrongSequence, "account sequence mismatch")
}

// TestTx_badSequenceRefused: a transaction signed for a sequence ahead of (or
// behind) the account's is refused by CheckTx.
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
		signed := c.Sign(t, k, chain.TxOptions{Offline: true, AccountNumber: acc.Number, Sequence: seq}, harmlessMsg(t, c, k))
		r := c.Broadcast(t, k.Node, signed)
		chain.RequireCode(t, "sequence out of order", r, sdkSpace, codeWrongSequence, "account sequence mismatch")
	}
}

// TestTx_wrongAccountNumberRefused: the signature commits to the account
// number; another one fails signature verification.
func TestTx_wrongAccountNumberRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	acc, _ := c.AccountOf(t, k.Node, k.Address)
	signed := c.Sign(t, k, chain.TxOptions{Offline: true, AccountNumber: acc.Number + 1000, Sequence: acc.Sequence}, harmlessMsg(t, c, k))
	r := c.Broadcast(t, k.Node, signed)
	chain.RequireCode(t, "wrong account number", r, sdkSpace, codeSigVerifyFailed, "signature verification failed")
}

// TestTx_wrongChainIDRefused: a signature over another chain id (still a
// devnet id: nothing here signs for a real network) fails verification.
func TestTx_wrongChainIDRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	signed := c.Sign(t, k, chain.TxOptions{ChainID: c.ID + "-other"}, harmlessMsg(t, c, k))
	r := c.Broadcast(t, k.Node, signed)
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

// TestTx_tamperedBodyRefused: changing one byte of a signed body (the memo)
// breaks the signature.
func TestTx_tamperedBodyRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	signed := c.Sign(t, k, chain.TxOptions{Memo: "e2e original"}, harmlessMsg(t, c, k))
	var tx map[string]any
	if err := json.Unmarshal(signed, &tx); err != nil {
		t.Fatal(err)
	}
	tx["body"].(map[string]any)["memo"] = "e2e tampered"
	forged, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Broadcast(t, k.Node, forged)
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
