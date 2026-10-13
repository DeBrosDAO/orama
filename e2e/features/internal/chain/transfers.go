//go:build e2e_fleet

package chain

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// TransferNorama is what each transfer pays the payee.
	TransferNorama = 1
	// transferFunds is what the faucet gives the payer, in ORAMA: far more than
	// the fees of a batch (DefaultGas at the floor base fee is 0.0006 ORAMA).
	transferFunds = 5
)

// TransferLoad is a batch of public transfers (cosmos bank MsgSend) from one
// funded key to another, signed ahead with consecutive sequences and left on
// the payer's node, then broadcast in ranges, so a test can keep submitting
// while it breaks something. Like PrepareSigned it takes no key lock between
// signing and broadcasting: only a package that runs alone may use it.
type TransferLoad struct {
	c     *Chain
	Payer Key
	Payee Key
	dir   string
	first uint64
	total int
}

// NewTransferLoad funds a payer on n through the faucet, creates a payee and
// signs total transfers. name (lowercase letters, digits, hyphens) prefixes the
// two throwaway keys. The batch's files and keys are removed at cleanup.
func (c *Chain) NewTransferLoad(t testing.TB, n fleet.Node, name string, total int) *TransferLoad {
	t.Helper()
	payer := c.NewFundedKey(t, n, name+"-payer", Orama(transferFunds))
	payee := c.NewKey(t, n, name+"-payee")
	acc, ok := c.AccountOf(t, n, payer.Address)
	if !ok {
		t.Fatalf("%s has no account after a faucet drip", payer.Address)
	}
	send := NewMsg("/cosmos.bank.v1beta1.MsgSend", map[string]any{
		"from_address": payer.Address, "to_address": payee.Address,
		"amount": []any{map[string]any{"denom": Denom, "amount": fmt.Sprint(TransferNorama)}},
	})
	dir := c.PrepareSigned(t, payer, TxOptions{}, []Msg{send}, total)
	return &TransferLoad{c: c, Payer: payer, Payee: payee, dir: dir, first: acc.Sequence, total: total}
}

// Total is how many transfers the batch holds.
func (l *TransferLoad) Total() int { return l.total }

// rangeBroadcastScript sends the batch's files from..to (1-based, inclusive)
// to the payer's node's loopback RPC with broadcast_tx_sync, in order, and
// prints one CheckTx code per transaction.
func (l *TransferLoad) rangeBroadcastScript(from, to int) string {
	return "D=" + fleet.ShellQuote(l.dir) + `
for f in $(ls "$D"/tx*.b64 | sed -n '` + fmt.Sprintf("%d,%dp", from, to) + `'); do
  printf '{"jsonrpc":"2.0","id":1,"method":"broadcast_tx_sync","params":{"tx":"%s"}}' "$(tr -d '\n' < "$f")" > "$D/req.json"
  curl -sS --max-time 20 -H 'Content-Type: application/json' --data-binary @"$D/req.json" ` + l.c.RPCHTTP() + ` |
    python3 -c 'import json,sys;d=json.load(sys.stdin);r=d.get("result") or {};print("code", r.get("code", "rpc-error:"+json.dumps(d.get("error"))))'
done
`
}

// Broadcast sends transfers [from, to) to the payer's node and returns the
// CheckTx code of each ("0": accepted into its mempool). to is capped at the
// batch's size.
func (l *TransferLoad) Broadcast(t testing.TB, from, to int) []string {
	t.Helper()
	to = min(to, l.total)
	if from < 0 || from >= to {
		return nil
	}
	out := l.c.Run(t, l.Payer.Node, TxBudget, l.rangeBroadcastScript(from+1, to))
	if out.Exit != 0 {
		t.Fatal(l.c.F.Redact(fmt.Sprintf("%s: broadcasting transfers %d..%d exited %d: %s", l.Payer.Node.Name, from, to, out.Exit, out.Stderr)))
	}
	return strings.Fields(strings.ReplaceAll(out.Stdout, "code", ""))
}

// RequireAccepted fails unless every code is 0, the mempool's acceptance.
func RequireAccepted(t testing.TB, what string, codes []string) {
	t.Helper()
	for i, code := range codes {
		if code != "0" {
			t.Fatalf("%s: transfer %d of the range was refused by CheckTx with %q", what, i, code)
		}
	}
}

// Delivered is how many of the batch's transfers are in blocks as observer
// sees them: its payer's sequence past the first.
func (l *TransferLoad) Delivered(t testing.TB, observer fleet.Node) int {
	t.Helper()
	acc, ok := l.c.AccountOf(t, observer, l.Payer.Address)
	if !ok {
		t.Fatalf("%s: the payer %s has no account", observer.Name, l.Payer.Address)
	}
	return int(acc.Sequence - l.first)
}

// WaitDelivered waits until the first n transfers of the batch are in blocks
// as observer sees them.
func (l *TransferLoad) WaitDelivered(t testing.TB, observer fleet.Node, n int) {
	t.Helper()
	l.c.WaitSequence(t, observer, l.Payer.Address, l.first+uint64(n))
}

// RequireBalance fails unless observer sees the payee holding exactly n
// transfers' worth.
func (l *TransferLoad) RequireBalance(t testing.TB, observer fleet.Node, n int) {
	t.Helper()
	want := NewInt(int64(n) * TransferNorama)
	if got := l.c.Bank(t, observer, l.Payee.Address); got.Cmp(want) != 0 {
		t.Errorf("%s: the payee holds %s norama after %d transfers of %d, want %s", observer.Name, got.String(), n, TransferNorama, want.String())
	}
}
