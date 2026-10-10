//go:build e2e_fleet

package chaintransfers

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	// privateRefusal is the start of onchain.ErrPrivateUnavailable.
	privateRefusal = "a private transfer needs the RootWallet to build the shielded bundle"
	// explicitPublic is how that refusal names the alternative.
	explicitPublic = "choose a public transfer explicitly"
	// oneNorama is 0.000000001 ORAMA, the smallest amount.
	oneNorama = "0.000000001"
)

func run(t *testing.T, args ...string) oramacli.Result {
	t.Helper()
	return infra.Run(t, harness.CLI(t), args...)
}

// TestSend_withoutPublicIsPrivateAndPaysNothing: no --public is a private send, which the
// RootWallet cannot build yet; the command refuses, names the explicit alternative, and a fresh
// recipient still holds nothing. --yes does not make it public.
func TestSend_withoutPublicIsPrivateAndPaysNothing(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, chain.OperatorNode)
	payee := c.NewKey(t, n, "e2e-transfers-private")
	for _, args := range [][]string{
		{"chain", "send", payee.Address, oneNorama},
		{"chain", "send", payee.Address, "1", "--yes"},
	} {
		infra.ExpectExit(t, run(t, args...), infra.ExitFailure, privateRefusal, explicitPublic)
	}
	if got := c.Bank(t, n, payee.Address); !got.IsZero() {
		t.Errorf("a refused private send paid %s norama", got.String())
	}
}

// TestSend_refusalsAreUsageErrorsBeforeAnyWalletOrChain: the CLI judges the recipient and the
// amount itself.
func TestSend_refusalsAreUsageErrorsBeforeAnyWalletOrChain(t *testing.T) {
	t.Parallel()
	valid := "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	cases := map[string][]string{
		"not an address":     {"chain", "send", "bob", "1", "--public", "--yes"},
		"a valoper address":  {"chain", "send", "oramavaloper19rl4cm2hmr8afy4kldpxz3fka4jguq0al2xuls", "1", "--public", "--yes"},
		"zero":               {"chain", "send", valid, "0", "--public", "--yes"},
		"negative":           {"chain", "send", valid, "-1", "--public", "--yes"},
		"an exponent":        {"chain", "send", valid, "1e9", "--public", "--yes"},
		"ten decimals":       {"chain", "send", valid, "0.0000000001", "--public", "--yes"},
		"no amount":          {"chain", "send", valid, "--public"},
		"surplus argument":   {"chain", "send", valid, "1", "extra", "--public"},
		"withdraw zero":      {"chain", "withdraw-earnings", "0"},
		"withdraw all":       {"chain", "withdraw-earnings", "all"},
		"withdraw fractions": {"chain", "withdraw-earnings", "0.0000000001"},
		"withdraw nothing":   {"chain", "withdraw-earnings"},
	}
	for name, args := range cases {
		res := run(t, args...)
		if res.Exit != infra.ExitUsage {
			t.Errorf("%s: exit %d, want a usage error\n%s%s", name, res.Exit, res.Stdout, res.Stderr)
		}
	}
}

// TestSend_publicStopsAtAnAccountTheChainHasNotSeen: a public send and a withdrawal read the
// chain id and the signer's account through the gateway. The run agent's account has never
// received funds, so the chain has no such account: the command fails naming that, or the
// headless agent refuses to show the account; either way nothing is signed or broadcast.
func TestSend_publicStopsAtAnAccountTheChainHasNotSeen(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, chain.OperatorNode)
	payee := c.NewKey(t, n, "e2e-transfers-public")
	for _, args := range [][]string{
		{"chain", "send", payee.Address, "1", "--public", "--yes"},
		{"chain", "withdraw-earnings", "1"},
	} {
		res := run(t, args...)
		out := res.Stdout + res.Stderr
		if res.Exit == infra.ExitOK {
			t.Fatalf("orama %s succeeded for an account that holds nothing\n%s", strings.Join(args, " "), out)
		}
		if !strings.Contains(out, "does not exist on the chain yet") && !strings.Contains(out, "rootwallet agent") {
			t.Errorf("orama %s: the failure names neither the unfunded account nor the agent\n%s", strings.Join(args, " "), out)
		}
	}
	if got := c.Bank(t, n, payee.Address); !got.IsZero() {
		t.Errorf("a send that could not be signed paid %s norama", got.String())
	}
}
