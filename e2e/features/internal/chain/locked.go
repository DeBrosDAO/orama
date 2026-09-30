//go:build e2e_fleet

package chain

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Markers of the sequences ReplayLocked prints around its broadcast.
const (
	markSeqBefore = "__E2E_SEQ_BEFORE__"
	markSeqAfter  = "__E2E_SEQ_AFTER__"
)

// accountPy prints an account's number and sequence from `oramad query auth
// account` JSON. proto3 JSON omits a zero, so a missing member is 0; an
// output without the account's address is not an account and fails.
const accountPy = `import json,sys
def f(v,k):
    if isinstance(v,dict):
        if isinstance(v.get(k),(str,int)): return str(v[k])
        v=list(v.values())
    if isinstance(v,list):
        for c in v:
            r=f(c,k)
            if r is not None: return r
    return None
d=json.load(sys.stdin)
if f(d,"address") is None: sys.exit("no account in the query output")
print(f(d,"account_number") or "0", f(d,"sequence") or "0")`

// LockedEdit is what SignAndBroadcastLocked changes around the signature.
type LockedEdit struct {
	// AccountNumberDelta is added to the live account number the signature
	// commits to (the sequence is always the live one).
	AccountNumberDelta uint64
	// Memo, when not empty, replaces the body's memo after signing.
	Memo string
}

// accountScript reads k's live account number and sequence into $AN and $SEQ.
func (c *Chain) accountScript(k Key) string {
	q := OramadCmd("query", "auth", "account", k.Address, "--node", c.RPC(), "--output", "json")
	return fmt.Sprintf("A=$(%s | python3 -c %s) || exit 95\nset -- $A; AN=$1; SEQ=$2\n", q, fleet.ShellQuote(accountPy))
}

// SignAndBroadcastLocked signs msgs with k for its live account number (plus
// edit.AccountNumberDelta) and sequence, applies edit, and broadcasts, all
// under k's lock: no other transaction of k can take the sequence between
// the read and the broadcast, so a refusal is about what the test built, not
// a sequence race.
func (c *Chain) SignAndBroadcastLocked(t testing.TB, k Key, opts TxOptions, edit LockedEdit, msgs ...Msg) Result {
	t.Helper()
	dir, err := c.stageDir(t, k.Node, "u.json", c.checkedTx(t, opts, msgs))
	if err != nil {
		t.Fatal(err)
	}
	// Signed offline with placeholder zeros, swapped for the live values the
	// script reads under the lock.
	opts.Offline, opts.AccountNumber, opts.Sequence = true, 0, 0
	sign := signScript(c, k, opts)
	sign = strings.Replace(sign, "'--account-number' '0'", `'--account-number' "$AN"`, 1)
	sign = strings.Replace(sign, "'--sequence' '0'", `'--sequence' "$SEQ"`, 1)
	if !strings.Contains(sign, `"$AN"`) || !strings.Contains(sign, `"$SEQ"`) {
		t.Fatalf("the sign command has no offline account flags to fill: %s", sign)
	}
	script := "D=" + fleet.ShellQuote(dir) + "\n" + lockScript(k) + c.feeScript(opts) + c.accountScript(k) +
		fmt.Sprintf("AN=$((AN + %d))\n", edit.AccountNumberDelta) + sign
	if edit.Memo != "" {
		script += fmt.Sprintf("python3 -c %s \"$D/s.json\" %s || exit 96\n",
			fleet.ShellQuote(`import json,sys;p=sys.argv[1];d=json.load(open(p));d["body"]["memo"]=sys.argv[2];json.dump(d,open(p,"w"))`),
			fleet.ShellQuote(edit.Memo))
	}
	r, err := c.runTxScript(t, k.Node, script+c.broadcastScript())
	if err != nil {
		t.Fatal(c.F.Redact(err.Error()))
	}
	return r
}

// ReplayLocked broadcasts an already signed transaction of k again from k's
// node, under k's lock, and returns the result with k's sequence read right
// before and right after it: with the lock held, only the replay itself
// could have moved it.
func (c *Chain) ReplayLocked(t testing.TB, k Key, signed []byte) (r Result, before, after uint64) {
	t.Helper()
	dir, err := c.stageDir(t, k.Node, "s.json", signed)
	if err != nil {
		t.Fatal(err)
	}
	script := "D=" + fleet.ShellQuote(dir) + "\n" + lockScript(k) +
		c.accountScript(k) + "echo " + markSeqBefore + "; echo \"$SEQ\"\n" +
		"(\n" + c.broadcastScript() + ")\n" +
		c.accountScript(k) + "echo " + markSeqAfter + "; echo \"$SEQ\"\n"
	out, err := c.run(t, k.Node, TxBudget, script)
	if err != nil || out.Exit != 0 {
		t.Fatal(c.F.Redact(fmt.Sprintf("%s: replay script exited %d: %v %s", k.Node.Name, out.Exit, err, out.Stderr)))
	}
	s := sections(out.Stdout)
	if r, err = parseResult(s); err != nil {
		t.Fatal(c.F.Redact(err.Error()))
	}
	if before, err = strconv.ParseUint(strings.TrimSpace(s[markSeqBefore]), 10, 64); err != nil {
		t.Fatalf("sequence before the replay: %v", err)
	}
	if after, err = strconv.ParseUint(strings.TrimSpace(s[markSeqAfter]), 10, 64); err != nil {
		t.Fatalf("sequence after the replay: %v", err)
	}
	r.Signed = signed
	return r, before, after
}
