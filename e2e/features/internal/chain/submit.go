//go:build e2e_fleet

package chain

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

var digits = regexp.MustCompile(`^[0-9]+$`)

// Submit signs msgs with k on k's node, broadcasts them to its loopback RPC
// and waits for a block, holding k's lock from the fee read to the block so
// no other package's transaction takes the same sequence. It returns what
// happened; it fails the test only when the transaction could not be built,
// signed or its answer read.
func (c *Chain) Submit(t testing.TB, k Key, opts TxOptions, msgs ...Msg) Result {
	t.Helper()
	raw := c.checkedTx(t, opts, msgs)
	r, err := c.submit(t, k, opts, raw)
	if err != nil {
		t.Fatal(c.F.Redact(err.Error()))
	}
	return r
}

// SubmitUnsigned is Submit for an unsigned transaction some other tool built
// (proto-JSON, e.g. from DecodeTxRaw): its body is kept byte for byte in
// meaning, its fee is replaced by the one opts computes, and k signs it.
func (c *Chain) SubmitUnsigned(t testing.TB, k Key, opts TxOptions, unsigned []byte) Result {
	t.Helper()
	var tx map[string]any
	if err := json.Unmarshal(unsigned, &tx); err != nil {
		t.Fatalf("unsigned transaction is not JSON: %v", err)
	}
	auth, _ := tx["auth_info"].(map[string]any)
	if camel, ok := tx["authInfo"].(map[string]any); ok && auth == nil {
		auth = camel
		delete(tx, "authInfo")
		tx["auth_info"] = auth
	}
	if auth == nil {
		t.Fatalf("unsigned transaction has no auth_info: %s", unsigned)
	}
	delete(auth, "signerInfos")
	auth["signer_infos"] = []any{}
	if fee, ok := auth["fee"].(map[string]any); ok {
		fee["gas_limit"] = fmt.Sprint(opts.gas())
		fee["granter"] = opts.Granter
	}
	tx["signatures"] = []any{}
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.submit(t, k, opts, raw)
	if err != nil {
		t.Fatal(c.F.Redact(err.Error()))
	}
	return r
}

// DecodeTxRaw turns the protobuf TxRaw bytes (body, auth info, no signature)
// into the proto-JSON oramad works with, using the node's own decoder.
func (c *Chain) DecodeTxRaw(t testing.TB, n fleet.Node, raw []byte) []byte {
	t.Helper()
	out := c.Run(t, n, QueryBudget, c.OramadCmd("tx", "decode", base64.StdEncoding.EncodeToString(raw)))
	if out.Exit != 0 {
		t.Fatalf("%s: oramad tx decode refused the transaction: %s", n.Name, out.Stderr)
	}
	return []byte(strings.TrimSpace(out.Stdout))
}

// CleanupSubmit is Submit for a t.Cleanup: it reports a transaction that
// could not be sent or did not succeed with t.Errorf and never stops the
// remaining cleanups.
func (c *Chain) CleanupSubmit(t testing.TB, k Key, what string, msgs ...Msg) {
	t.Helper()
	raw, err := unsignedTx(TxOptions{}, msgs)
	if err == nil {
		var r Result
		if r, err = c.submit(t, k, TxOptions{}, raw); err == nil && !r.OK() {
			err = fmt.Errorf("refused: %s", r)
		}
	}
	if err != nil {
		t.Errorf("cleanup: %s: %s", what, c.F.Redact(err.Error()))
	}
}

func (c *Chain) submit(t testing.TB, k Key, opts TxOptions, raw []byte) (Result, error) {
	t.Helper()
	dir, err := c.stageDir(t, k.Node, "u.json", raw)
	if err != nil {
		return Result{}, err
	}
	script := "D=" + fleet.ShellQuote(dir) + "\n" + lockScript(k) + c.feeScript(opts) + signScript(c, k, opts) + c.broadcastScript()
	return c.runTxScript(t, k.Node, script)
}

// Sign signs msgs with k and returns the signed transaction without
// broadcasting it: the raw material of replay, tamper and wrong-chain tests.
func (c *Chain) Sign(t testing.TB, k Key, opts TxOptions, msgs ...Msg) []byte {
	t.Helper()
	s := c.signOnly(t, k, opts, msgs)
	if e, ok := s[markSignErr]; ok {
		t.Fatalf("%s: oramad tx sign refused: %s", k.Node.Name, strings.TrimSpace(e))
	}
	signed := strings.TrimSpace(s[markSigned])
	if signed == "" {
		t.Fatalf("%s: oramad tx sign printed no transaction", k.Node.Name)
	}
	return []byte(signed)
}

// SignExpectRefused runs `oramad tx sign` for a transaction the client
// itself must refuse (e.g. a message whose signer is not the key) and
// returns the client's error text.
func (c *Chain) SignExpectRefused(t testing.TB, k Key, opts TxOptions, msgs ...Msg) string {
	t.Helper()
	s := c.signOnly(t, k, opts, msgs)
	e, ok := s[markSignErr]
	if !ok {
		t.Fatalf("%s: oramad tx sign signed a transaction it must refuse: %s", k.Node.Name, s[markSigned])
	}
	return strings.TrimSpace(e)
}

func (c *Chain) signOnly(t testing.TB, k Key, opts TxOptions, msgs []Msg) map[string]string {
	t.Helper()
	dir, err := c.stageDir(t, k.Node, "u.json", c.checkedTx(t, opts, msgs))
	if err != nil {
		t.Fatal(err)
	}
	script := "D=" + fleet.ShellQuote(dir) + "\ntrap 'rm -rf -- \"$D\"' EXIT\n" + c.feeScript(opts) + signScript(c, k, opts)
	out := c.Run(t, k.Node, TxBudget, script)
	if out.Exit != 0 {
		t.Fatal(c.F.Redact(fmt.Sprintf("%s: signing exited %d: %s", k.Node.Name, out.Exit, out.Stderr)))
	}
	return sections(out.Stdout)
}

// Broadcast sends an already signed transaction from node n and waits for a
// block. It takes no lock: it is for transactions a test built on purpose
// (a replay, a tampered body, a wrong chain id).
func (c *Chain) Broadcast(t testing.TB, n fleet.Node, signed []byte) Result {
	t.Helper()
	dir, err := c.stageDir(t, n, "s.json", signed)
	if err != nil {
		t.Fatal(err)
	}
	script := "D=" + fleet.ShellQuote(dir) + "\ntrap 'rm -rf -- \"$D\"' EXIT\n" + c.broadcastScript()
	r, err := c.runTxScript(t, n, script)
	if err != nil {
		t.Fatal(c.F.Redact(err.Error()))
	}
	r.Signed = signed
	return r
}

func (c *Chain) checkedTx(t testing.TB, opts TxOptions, msgs []Msg) []byte {
	t.Helper()
	if opts.Mode == FeeAbsolute && !digits.MatchString(opts.FeeAmount) {
		t.Fatalf("FeeAmount %q must be a non-negative integer of norama", opts.FeeAmount)
	}
	if opts.ChainID != "" {
		if err := checkChainID(c.F.State, opts.ChainID); err != nil {
			t.Fatalf("refusing to sign for chain %q: %v", opts.ChainID, err)
		}
	}
	raw, err := unsignedTx(opts, msgs)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (c *Chain) runTxScript(t testing.TB, n fleet.Node, script string) (Result, error) {
	t.Helper()
	out, err := c.run(t, n, TxBudget, script)
	if err != nil {
		return Result{}, fmt.Errorf("%s: transaction script could not run: %w", n.Name, err)
	}
	if out.Exit != 0 {
		return Result{}, fmt.Errorf("%s: transaction script exited %d\nstdout: %s\nstderr: %s", n.Name, out.Exit, out.Stdout, out.Stderr)
	}
	return parseResult(sections(out.Stdout))
}

// Account is an x/auth account's number and sequence.
type Account struct {
	Number   uint64
	Sequence uint64
}

// AccountOf reads addr's account number and sequence, or reports that the
// account does not exist (it has never received anything).
func (c *Chain) AccountOf(t testing.TB, n fleet.Node, addr string) (Account, bool) {
	t.Helper()
	out := c.QueryOut(t, n, "auth", "account", addr)
	if out.Exit != 0 {
		if strings.Contains(out.Stderr+out.Stdout, "not found") {
			return Account{}, false
		}
		t.Fatalf("%s: auth account %s exited %d: %s", n.Name, addr, out.Exit, out.Stderr)
	}
	var doc any
	if err := json.Unmarshal([]byte(out.Stdout), &doc); err != nil {
		t.Fatalf("auth account %s: %v: %s", addr, err, out.Stdout)
	}
	// proto3 JSON omits zero values: the first account ever created has
	// account_number 0 and no member for it, and a fresh one sequence 0.
	if _, ok := findField(doc, "address"); !ok {
		t.Fatalf("auth account %s: the output holds no account: %s", addr, out.Stdout)
	}
	num, okN := findField(doc, "account_number")
	seq, okS := findField(doc, "sequence")
	a := Account{}
	var err error
	if okN {
		if a.Number, err = Uint(num); err != nil {
			t.Fatal(err)
		}
	}
	if okS {
		if a.Sequence, err = Uint(seq); err != nil {
			t.Fatal(err)
		}
	}
	return a, true
}

// findField finds the first member named key at any depth and returns it as
// a string (the account is an Any whose JSON shape depends on the encoder).
func findField(v any, key string) (string, bool) {
	switch x := v.(type) {
	case map[string]any:
		if val, ok := x[key]; ok {
			switch s := val.(type) {
			case string:
				return s, true
			case float64:
				return fmt.Sprintf("%.0f", s), true
			}
		}
		for _, child := range x {
			if s, ok := findField(child, key); ok {
				return s, true
			}
		}
	case []any:
		for _, child := range x {
			if s, ok := findField(child, key); ok {
				return s, true
			}
		}
	}
	return "", false
}
