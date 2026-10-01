//go:build e2e_fleet

package clistorageglobal

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
)

// unreachableREST is a chain REST address nothing listens on.
const unreachableREST = "http://127.0.0.1:1"

// refusal is one bad transaction command line and the reason it must give.
type refusal struct {
	label string
	args  []string
	want  string
}

func withSigner(flag string, args ...string) []string {
	return append(args, signerFlags(flag)...)
}

// without returns args with flag and its value removed.
func without(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// replace returns args with flag's value set to value.
func replace(args []string, flag, value string) []string {
	out := append([]string{}, args...)
	for i := range out {
		if out[i] == flag && i+1 < len(out) {
			out[i+1] = value
		}
	}
	return out
}

func txRefusals(t testing.TB) []refusal {
	t.Helper()
	retire := withSigner("--operator", "global", "retire", "--id", nodeID)
	nonce := strings.Repeat("ab", 32)
	piece := strings.Repeat("cd", 32) + ":1024"
	create := func(class, n, p string) []string {
		return withSigner("--signer", "storage", "create", "--class", class, "--nonce", n, "--price", "10", "--duration-epochs", "5", "--piece", p)
	}
	register := func(domain string, extra ...string) []string {
		return withSigner("--operator", append([]string{"cluster", "register-onchain", "--id", "e2e-cluster", "--base-domain", domain}, extra...)...)
	}
	return []refusal{
		{"no chain id", without(retire, "--chain-id"), "chain id"},
		{"pubkey not hex", replace(retire, "--pubkey", "zz"), "pubkey is not hex"},
		{"pubkey not compressed", replace(retire, "--pubkey", strings.Repeat("04", 33)), "pubkey"},
		{"zero fee", replace(retire, "--fee", "0"), "fee must be a positive integer"},
		{"negative fee", replace(retire, "--fee", "-5"), "fee"},
		{"zero gas", replace(retire, "--gas", "0"), "gas must be positive"},
		{"uppercase operator", replace(retire, "--operator", strings.ToUpper(operator)), "operator"},
		{"no operator", without(retire, "--operator"), "operator"},
		{"path traversal id", replace(retire, "--id", "../../x"), "must match"},
		{"unicode id", replace(retire, "--id", "node\u202e1"), "must match"},
		{"private endpoint", register("e2e.example.com", "--endpoint", "http://10.0.0.1"), "not a public address"},
		{"no endpoint", register("e2e.example.com"), "endpoints"},
		{"http metadata", register("e2e.example.com", "--endpoint", "https://e2e.example.com", "--metadata-uri", "http://e2e.example.com/m"), "https"},
		{"ip base domain", register("1.2.3.4", "--endpoint", "https://e2e.example.com"), "not an ip"},
		{"archive deal", create("archive", nonce, piece), "archive"},
		{"short nonce", create("public-pin", "ab", piece), "nonce"},
		{"bad piece", create("public-pin", nonce, "zz:10"), "piece"},
		{"private deal one piece", create("private", nonce, piece), "one piece per replica"},
		{"self grant", withSigner("--signer", "storage", "grant", "--grantee", operator, "--spend-limit", "1000", "--max-piece-bytes", "1", "--max-duration-epochs", "1"), "differ"},
		{"unknown role", withSigner("--operator", "global", "bond", "--id", nodeID, "--role", "bogus", "--amount", "1"), "unknown role"},
		{"negative amount", withSigner("--operator", "global", "bond", "--id", nodeID, "--role", "storage", "--amount", "-1"), "amount"},
		{"no deal id", withSigner("--signer", "storage", "extend", "--extra-epochs", "2"), "deal id"},
		{"short proof leaf", withSigner("--signer", "storage", "prove", "--id", nodeID, "--file", badProofFile(t)), "leaf"},
	}
}

// badProofFile is a proof whose leaf is not the 1024 bytes a leaf is.
func badProofFile(t testing.TB) string {
	t.Helper()
	raw, err := json.Marshal([]map[string]any{{"deal_id": 1, "slot": 0, "leaf_index": 0,
		"leaf": hex.EncodeToString(make([]byte, 10)), "siblings": []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bad-proofs.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTxCommands_invalidValuesAreUsage: every stateless rule the chain would
// apply is checked on the command line first, as a usage error (exit 2) with
// the reason, before any document is printed or any node is asked.
func TestTxCommands_invalidValuesAreUsage(t *testing.T) {
	t.Parallel()
	cli := cliNoWallet(t)
	for _, r := range txRefusals(t) {
		res := run(t, cli, r.args...)
		if res.Exit != exitUsage || !strings.Contains(output(res), r.want) {
			t.Errorf("%s: exit %d, want %d and %q\n%s", r.label, res.Exit, exitUsage, r.want, output(res))
		}
		if strings.Contains(res.Stdout, signDocHeader) {
			t.Errorf("%s: a sign document was printed for a refused command line", r.label)
		}
	}
}

// TestTxCommands_unreadableFileIsFailure: a proof file that cannot be read
// is refused naming it, and nothing is printed.
func TestTxCommands_unreadableFileIsFailure(t *testing.T) {
	t.Parallel()
	res := run(t, cliNoWallet(t), withSigner("--signer", "storage", "prove", "--id", nodeID, "--file", "/nonexistent/proofs.json")...)
	infra.ExpectRefused(t, res, "/nonexistent/proofs.json")
}

// TestTxCommands_unreachableChainIsUnavailable: with --node naming a chain
// REST API that does not answer, the command stops before signing, with the
// "could not be reached" code (5) a script may retry on (clierr CodeUnavailable).
func TestTxCommands_unreachableChainIsUnavailable(t *testing.T) {
	t.Parallel()
	args := append(withSigner("--operator", "global", "retire", "--id", nodeID), "--node", unreachableREST)
	res := run(t, cliNoWallet(t), args...)
	infra.ExpectExit(t, res, infra.ExitUnavailable, "read the chain account")
}
