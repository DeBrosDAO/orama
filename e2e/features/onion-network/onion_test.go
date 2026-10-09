//go:build e2e_fleet

package onionnetwork

import (
	"os"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	// checkBudget covers bootstrapping tor on the network and a rendezvous
	// circuit to each validator onion service.
	checkBudget = 8 * time.Minute

	docFee = "1000"
	docGas = "200000"
	// operator is a well-formed address; the tests that never reach the chain do
	// not need it to exist.
	operator = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"

	// Whether the run's RootWallet agent signs orama-tx (RootWallet task 2857);
	// see features/chain-global.
	envOramaTxSigning = "E2E_ORAMA_TX_SIGNING"
	oramaTxRefused    = "ORAMA_TX_REFUSED"
	agentSignedAs     = "the agent signed as"
)

// retireArgs is a validator-retire command, the cheapest chain command that
// reads the operator's account before it signs.
func retireArgs(chainID, who string, extra ...string) []string {
	return append([]string{"global", "retire", "--operator", who, "--id", "e2e-none", "--chain-id", chainID, "--fee", docFee, "--gas", docGas}, extra...)
}

func TestOnion_aNetworkThatCannotBeJoinedSendsNothing(t *testing.T) {
	t.Parallel()
	// "false" exits at once, as a tor that cannot reach any authority does.
	args := retireArgs("e2e-chain", operator, "--onion-network", networkFile(t, true), "--onion-tor", "false", "--onion", sampleOnion)
	res := infra.Run(t, harness.CLI(t), args...)
	infra.ExpectExit(t, res, infra.ExitUnavailable, "the transaction was not sent")
}

func TestOnion_aNodeAndTheNetworkAreTwoRoutes(t *testing.T) {
	t.Parallel()
	args := retireArgs("e2e-chain", operator, "--onion-network", networkFile(t, true), "--node", "http://127.0.0.1:9")
	infra.ExpectExit(t, infra.Run(t, harness.CLI(t), args...), infra.ExitUsage, "pass one")
}

func TestOnion_aSecondTorClientIsRefused(t *testing.T) {
	t.Parallel()
	args := retireArgs("e2e-chain", operator, "--onion-network", networkFile(t, true), "--onion-socks", "127.0.0.1:9050")
	infra.ExpectExit(t, infra.Run(t, harness.CLI(t), args...), infra.ExitUsage, "two Tor clients")
}

func TestOnion_aPublicNetworkFileIsRefused(t *testing.T) {
	t.Parallel()
	args := retireArgs("e2e-chain", operator, "--onion-network", networkFile(t, false))
	infra.ExpectExit(t, infra.Run(t, harness.CLI(t), args...), infra.ExitUsage, "not launched")
}

func TestOnion_theNetworkFileCanComeFromTheEnvironment(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	cli.Env = []string{tornet.NetworkEnv + "=" + networkFile(t, false)}
	infra.ExpectExit(t, infra.Run(t, cli, retireArgs("e2e-chain", operator)...), infra.ExitUsage, "not launched")
}

// With the live Tor network, the account is read from a validator's onion
// service. The run's headless agent then refuses to sign (or, with
// E2E_ORAMA_TX_SIGNING=1, signs): either way the command got past the read it
// makes first, over the network and nowhere else.
func TestOnion_theAccountIsReadOverTheNetwork(t *testing.T) {
	file := liveNetwork(t)
	c := chain.New(t)
	k := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	res := infra.RunFor(t, harness.CLI(t), checkBudget, retireArgs(c.ID, k.Address, "--onion-network", file)...)
	want := oramaTxRefused
	if os.Getenv(envOramaTxSigning) == "1" {
		want = agentSignedAs
	}
	infra.ExpectExit(t, res, infra.ExitFailure, want)
}
