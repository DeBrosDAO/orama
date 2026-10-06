//go:build e2e_fleet

package chainfaucet

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// signer is the node whose operator key signs the drips (chain.FaucetNode).
func signer(t *testing.T, c *chain.Chain) fleet.Node { return c.FaucetNode(t) }

// faucetParams are x/emission's faucet parameters (docs/CHAIN.md).
type faucetParams struct {
	Params struct {
		Enabled  bool      `json:"faucet_enabled"`
		MaxDrip  chain.Int `json:"faucet_max_drip"`
		Cooldown chain.Int `json:"faucet_recipient_cooldown_seconds"`
	} `json:"params"`
}

func readParams(t *testing.T, c *chain.Chain, n fleet.Node) faucetParams {
	t.Helper()
	var p faucetParams
	c.Query(t, n, &p, "emission", "params")
	if !p.Params.Enabled || p.Params.MaxDrip.IsZero() {
		t.Fatalf("the run chain's genesis did not enable the faucet: %+v (chain-deploy.sh and stagenet deploy.sh set faucet_enabled)", p.Params)
	}
	return p
}

const (
	cooldownRefusal = "still within its cooldown"
	maxDripRefusal  = "at most faucet_max_drip"
)

// faucetDoc is `orama chain faucet --json`.
type faucetDoc struct {
	TxHash    string `json:"tx_hash"`
	Recipient string `json:"recipient"`
	Amount    string `json:"amount_norama"`
	Balance   string `json:"recipient_balance_norama"`
}

// TestFaucet_fundsAFreshKey: an account that never existed receives the drip
// and its bank balance is exactly the amount, as the node's own bank query
// and as the CLI report it; the report names the transaction.
func TestFaucet_fundsAFreshKey(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := signer(t, c)
	readParams(t, c, n)
	k := c.NewKey(t, n, "e2e-faucet-fresh")
	if bank := c.Bank(t, n, k.Address); !bank.IsZero() {
		t.Fatalf("a key nothing ever paid holds %s norama", bank.String())
	}
	amount := chain.Orama(10)
	res := infra.Run(t, harness.CLI(t), "chain", "faucet", k.Address, "--env", c.F.State.Env, "--node", n.PublicIP,
		"--amount", amount.String(), "--json")
	infra.ExpectExit(t, res, infra.ExitOK)
	var doc faucetDoc
	if err := oramacli.DecodeJSON(res, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.TxHash == "" || doc.Recipient != k.Address || doc.Amount != amount.String() || doc.Balance != amount.String() {
		t.Errorf("faucet report %+v, want a transaction hash and %s norama to %s", doc, amount.String(), k.Address)
	}
	if got := c.Bank(t, n, k.Address); got.Cmp(amount) != 0 {
		t.Errorf("bank balance %s norama, want %s", got.String(), amount.String())
	}
	c.RequireInvariants(t, "a faucet drip")
}

// TestFaucet_secondDripInTheCooldownIsRefused: a recipient dripped once is
// refused a second time inside faucet_recipient_cooldown_seconds, with the
// chain's own reason, and its balance stays at the first drip.
func TestFaucet_secondDripInTheCooldownIsRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := signer(t, c)
	p := readParams(t, c, n)
	if p.Params.Cooldown.IsZero() {
		t.Skip("the run chain has no faucet cooldown (faucet_recipient_cooldown_seconds is 0)")
	}
	k := c.NewKey(t, n, "e2e-faucet-cooldown")
	first := chain.Orama(1)
	c.Fund(t, n, k.Address, first)
	res := c.Faucet(t, k.Address, first)
	infra.ExpectExit(t, res, infra.ExitFailure, cooldownRefusal)
	if got := c.Bank(t, n, k.Address); got.Cmp(first) != 0 {
		t.Errorf("balance %s norama after a refused second drip, want %s", got.String(), first.String())
	}
}

// TestFaucet_maxDripBoundary: exactly faucet_max_drip is paid; one norama more
// is refused by the chain and pays nothing.
func TestFaucet_maxDripBoundary(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := signer(t, c)
	max := readParams(t, c, n).Params.MaxDrip
	over := c.NewKey(t, n, "e2e-faucet-over")
	res := c.Faucet(t, over.Address, max.Add(chain.NewInt(1)))
	infra.ExpectExit(t, res, infra.ExitFailure, maxDripRefusal)
	if got := c.Bank(t, n, over.Address); !got.IsZero() {
		t.Errorf("a refused over-max drip left %s norama", got.String())
	}
	exact := c.NewKey(t, n, "e2e-faucet-exact")
	if got := c.Fund(t, n, exact.Address, max); got.Cmp(max) != 0 {
		t.Errorf("a drip of exactly the maximum left %s norama, want %s", got.String(), max.String())
	}
}

// TestFaucet_refusesBadInputBeforeAnyTransaction: input the CLI can judge is a
// usage error (exit 2) and sends nothing: not an address, a wrong prefix, a
// zero, negative, fractional or oversized amount.
func TestFaucet_refusesBadInputBeforeAnyTransaction(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := signer(t, c)
	good := c.NewKey(t, n, "e2e-faucet-usage").Address
	cases := map[string][]string{
		"not an address":  {"chain", "faucet", "not-an-address"},
		"another prefix":  {"chain", "faucet", "cosmos1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53"},
		"zero amount":     {"chain", "faucet", good, "--amount", "0"},
		"negative amount": {"chain", "faucet", good, "--amount", "-5"},
		"fraction":        {"chain", "faucet", good, "--amount", "1.5"},
		"too large":       {"chain", "faucet", good, "--amount", strings.Repeat("9", 19)},
	}
	for name, args := range cases {
		res := infra.Run(t, harness.CLI(t), append(args, "--env", c.F.State.Env, "--node", n.PublicIP)...)
		if res.Exit != infra.ExitUsage {
			t.Errorf("%s: exit %d, want %d: %s", name, res.Exit, infra.ExitUsage, res.Stderr)
		}
	}
	if got := c.Bank(t, n, good); !got.IsZero() {
		t.Errorf("refused input still funded the account: %s norama", got.String())
	}
}
