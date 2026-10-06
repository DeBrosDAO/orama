//go:build e2e_fleet

package chainglobal

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// Whether the run's RootWallet agent signs orama-tx (RootWallet task 2857).
// Until it does, the headless agent refuses with ORAMA_TX_REFUSED
// (core/pkg/rwagent/errors.go); set E2E_ORAMA_TX_SIGNING=1 once it ships.
const (
	envOramaTxSigning = "E2E_ORAMA_TX_SIGNING"
	oramaTxRefused    = "ORAMA_TX_REFUSED"
	agentSignedAs     = "the agent signed as"
	restProbePath     = "/cosmos/base/tendermint/v1beta1/node_info"
)

// requireREST fails unless the node serves the chain REST API on its
// loopback 31003, which every --node command reads the account from
// (docs/CLI_REFERENCE.md "--node ... Chain REST API") and the gateway's
// chain proxy forwards to (docs/CHAIN.md "The gateway's chain proxy").
func requireREST(t *testing.T, c *chain.Chain) {
	t.Helper()
	if c.F.State.IsStagenet() {
		harness.SkipNotApplicable(t, "the stagenet chain REST API answers on "+c.Host()+" inside the orama-global netns, not on the node's 127.0.0.1:31003")
	}
	n := c.Node(t, chain.OperatorNode)
	out := c.F.Exec(t, n, fmt.Sprintf("curl -s -o /dev/null -w '%%{http_code}' --max-time 10 http://127.0.0.1:%d%s", chain.APIPort, restProbePath))
	if strings.TrimSpace(out.Stdout) != "200" {
		t.Fatalf("%s serves no chain REST API on 127.0.0.1:%d (got %q): e2e/scripts/chain-deploy.sh enables app.toml [api] and "+
			"`chain-deploy.sh status` checks it, so the deploy or the chain unit is broken; no --node command can read an account", n.Name, chain.APIPort, out.Stdout)
	}
}

// TestOnchainDocs_withNodeAsksRootWalletToSign: with --node (the chain REST
// API, reached through an SSH tunnel to the node's loopback) a chain command
// reads the account, builds the transaction and asks the RootWallet agent to
// sign it (wallet:sign:orama-tx). The run's headless agent refuses that
// today: the command fails with ORAMA_TX_REFUSED and nothing is broadcast.
// With E2E_ORAMA_TX_SIGNING=1 (after RootWallet task 2857) the agent signs,
// and the CLI refuses a signature from a key that is not the operator's.
func TestOnchainDocs_withNodeAsksRootWalletToSign(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	requireREST(t, c)
	s := newSigner(t, c, chain.OperatorNode)
	rest := "http://" + c.Tunnel(t, s.k.Node, chain.APIPort)
	id := chain.UniqueID(t, "e2e-rw-")
	res := infra.Run(t, harness.CLI(t), "cluster", "register-onchain", "--operator", s.k.Address, "--id", id,
		"--base-domain", c.F.State.BaseDomain, "--endpoint", c.F.State.GatewayURL, "--chain-id", c.ID,
		"--fee", docFee, "--gas", docGas, "--node", rest)
	want := oramaTxRefused
	if os.Getenv(envOramaTxSigning) == "1" {
		want = agentSignedAs
	}
	infra.ExpectExit(t, res, infra.ExitFailure, want)
	if out := c.QueryFails(t, s.k.Node, "nodes", "cluster", id); !chain.NotFound(out) {
		t.Errorf("cluster %s exists after a refused signature: %s", id, out)
	}
}

// TestOnchainDocs_withNodeUnreachableChainIsAFailure: a --node that answers
// nothing is a failure reading the account, before any signing.
func TestOnchainDocs_withNodeUnreachableChainIsAFailure(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	s := newSigner(t, c, chain.OperatorNode)
	res := infra.Run(t, harness.CLI(t), "global", "retire", "--operator", s.k.Address, "--id", "e2e-none", "--chain-id", c.ID,
		"--fee", docFee, "--gas", docGas, "--node", "http://127.0.0.1:9")
	infra.ExpectExit(t, res, infra.ExitFailure, "read the chain account")
}
