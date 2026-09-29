//go:build e2e_fleet

package chain

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Load fills blocks with gas so the x/fees base fee must rise
// (docs/CHAIN.md "The base fee (EIP-1559-style)": the target is 50% of the
// block's max_gas). Gas is consumed by transaction SIZE (x/auth charges 10
// gas per byte before any message runs, and a failed transaction's gas still
// counts against the block, cosmos-sdk baseapp runTx consumeBlockGas): each
// transaction is a MsgEditValidator of the signer's own validator whose
// description details is LoadDetailBytes long, which x/staking refuses after
// the ante chain has charged for every byte. So the load costs only fees
// (base_fee * gas, paid from earnings) and changes no state.
//
// Only a destructive package may call it: it raises the base fee every other
// transaction pays, and it takes no key lock between its two phases.
const (
	// LoadDetailBytes keeps one transaction under CometBFT's default
	// mempool max_tx_bytes (1 MiB).
	LoadDetailBytes = 900_000
	// LoadGas covers the size charge (about 10 gas per byte) and the
	// signature; ten of them fit the run chain's 100,000,000 block max_gas.
	LoadGas = 9_500_000
)

// LoadMsg is one load transaction's message for valoper.
func LoadMsg(valoper string) Msg {
	return NewMsg("/cosmos.staking.v1beta1.MsgEditValidator", map[string]any{
		"description": map[string]any{
			"moniker": "[do-not-modify]", "identity": "[do-not-modify]", "website": "[do-not-modify]",
			"security_contact": "[do-not-modify]", "details": strings.Repeat("x", LoadDetailBytes),
		},
		"validator_address": valoper,
	})
}

// PrepareLoad signs perKey load transactions for k offline, with
// consecutive sequences from the account's current one, each paying exactly
// base_fee*LoadGas at today's base fee, and leaves their encoded bytes in a
// directory on k's node. It returns that directory.
func (c *Chain) PrepareLoad(t testing.TB, k Key, perKey int) string {
	t.Helper()
	acc, ok := c.AccountOf(t, k.Node, k.Address)
	if !ok {
		t.Fatalf("%s has no account", k.Address)
	}
	raw := c.checkedTx(t, TxOptions{Gas: LoadGas}, []Msg{LoadMsg(c.Valoper(t, k))})
	dir, err := c.stageDir(t, k.Node, "u.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.cleanupDir(t, k.Node, dir) })
	var script strings.Builder
	script.WriteString("D=" + fleet.ShellQuote(dir) + "\nset -e\n")
	script.WriteString(feeScript(TxOptions{Gas: LoadGas}))
	for i := 0; i < perKey; i++ {
		opts := TxOptions{Offline: true, AccountNumber: acc.Number, Sequence: acc.Sequence + uint64(i)}
		sign := strings.Replace(signScript(c, k, opts), `"$D"'/s.json'`, fmt.Sprintf(`"$D"'/s%02d.json'`, i), 1)
		sign = sign[:strings.Index(sign, " 2> ")]
		enc := strings.ReplaceAll(OramadCmd("tx", "encode", fmt.Sprintf("$D/s%02d.json", i)), "'$D/", `"$D"'/`)
		fmt.Fprintf(&script, "%s\n%s > \"$D\"/tx%02d.b64\n", sign, enc, i)
	}
	out := c.Run(t, k.Node, TxBudget, script.String())
	if out.Exit != 0 {
		t.Fatal(c.F.Redact(fmt.Sprintf("%s: preparing the load exited %d: %s", k.Node.Name, out.Exit, out.Stderr)))
	}
	return dir
}

// broadcastLoadScript sends every prepared transaction, in sequence order,
// straight to CometBFT's broadcast_tx_sync (so each is in the mempool before
// the next arrives), and prints one CheckTx code per transaction.
func broadcastLoadScript(dir string) string {
	return "D=" + fleet.ShellQuote(dir) + `
for f in "$D"/tx*.b64; do
  printf '{"jsonrpc":"2.0","id":1,"method":"broadcast_tx_sync","params":{"tx":"%s"}}' "$(tr -d '\n' < "$f")" > "$D/req.json"
  curl -sS --max-time 20 -H 'Content-Type: application/json' --data-binary @"$D/req.json" ` + RPCHTTP + ` |
    python3 -c 'import json,sys;d=json.load(sys.stdin);r=d.get("result") or {};print("code", r.get("code", "rpc-error:"+json.dumps(d.get("error"))))'
done
`
}

// FireLoad broadcasts every prepared directory at once, one goroutine per
// node, and returns each node's CheckTx codes. It fails the test when a
// broadcast could not run.
func (c *Chain) FireLoad(t testing.TB, dirs map[Key]string) map[string][]string {
	t.Helper()
	var mu sync.Mutex
	var wg sync.WaitGroup
	codes := map[string][]string{}
	var errs []string
	for k, dir := range dirs {
		wg.Add(1)
		go func(k Key, dir string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), TxBudget)
			defer cancel()
			out, err := c.F.SSHFor(t, k.Node).Run(ctx, broadcastLoadScript(dir))
			mu.Lock()
			defer mu.Unlock()
			if err != nil || out.Exit != 0 {
				errs = append(errs, fmt.Sprintf("%s: exit %d %v %s", k.Node.Name, out.Exit, err, out.Stderr))
				return
			}
			codes[k.Node.Name] = strings.Fields(strings.ReplaceAll(out.Stdout, "code", ""))
		}(k, dir)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatal(c.F.Redact("load broadcast failed: " + strings.Join(errs, "; ")))
	}
	return codes
}
