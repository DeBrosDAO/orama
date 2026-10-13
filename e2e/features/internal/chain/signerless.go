//go:build e2e_fleet

package chain

import (
	"encoding/json"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// BroadcastUnsigned sends msgs from node n as a transaction with no signer
// info and no signature, which is what a signer-less x/shielded transfer is,
// and waits for the check (or the block). opts.Gas is the declared gas limit
// and opts.Memo the memo. The fee is empty unless opts.Mode is FeeAbsolute,
// which puts opts.FeeAmount norama in it (the signer-less path refuses a fee);
// FeeExact has no base fee to read here and is refused.
func (c *Chain) BroadcastUnsigned(t testing.TB, n fleet.Node, opts TxOptions, msgs ...Msg) Result {
	t.Helper()
	if opts.FeeAmount != "" && opts.Mode != FeeAbsolute {
		t.Fatalf("BroadcastUnsigned reads no base fee: a FeeAmount needs Mode FeeAbsolute, got %q", opts.Mode)
	}
	raw := c.checkedTx(t, opts, msgs)
	if opts.FeeAmount != "" {
		raw = withFee(t, raw, opts.FeeAmount)
	}
	return c.Broadcast(t, n, raw)
}

// withFee sets the fee amount of an unsigned proto-JSON transaction.
func withFee(t testing.TB, raw []byte, amount string) []byte {
	t.Helper()
	var tx map[string]any
	if err := json.Unmarshal(raw, &tx); err != nil {
		t.Fatalf("unsigned transaction is not JSON: %v", err)
	}
	auth, _ := tx["auth_info"].(map[string]any)
	fee, _ := auth["fee"].(map[string]any)
	if fee == nil {
		t.Fatalf("unsigned transaction has no auth_info.fee: %s", raw)
	}
	fee["amount"] = []any{map[string]any{"denom": Denom, "amount": amount}}
	out, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("failed to encode the transaction: %v", err)
	}
	return out
}
