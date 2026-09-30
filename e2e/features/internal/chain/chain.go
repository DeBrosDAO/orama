//go:build e2e_fleet

// Package chain is what the chain feature packages (chain-core,
// chain-economics, chain-services, chain-assets, chain-global,
// chain-shielded, chain-hotkey-archive, chain-cli, chain-waivers and their
// destructive packages) share: running the real
// oramad of a co-hosted validator on its node, building, signing and
// broadcasting transactions with the validator's own keyring, reading
// module queries, and the invariant check every step ends with.
//
// Route (why nothing here imports github.com/DeBrosOfficial/network/chain):
// the only funded accounts of a run chain are the three validator operator
// keys, and they live in each node's keyring (e2e/scripts/chain-deploy.sh:
// `keys add validator --keyring-backend test` under the chain home). A
// transaction is therefore signed ON the node, by the node's own
// `oramad tx sign`, from an unsigned transaction this package writes as
// proto-JSON (the codec of the real binary decodes every module's Msg), and
// broadcast with `oramad tx broadcast` to the node's loopback RPC. Queries
// are `oramad query <module> ... --output json`; the few Query RPCs without a
// CLI command go through CometBFT's `abci_query` (abci.go). The e2e module
// stays free of the cosmos-sdk dependency tree, and no key leaves a node.
package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Layout of a co-hosted validator (e2e/scripts/chain-deploy.sh, docs/CHAIN.md
// "The stagenet deploy script").
const (
	Oramad       = "/usr/lib/orama-global/bin/oramad"
	Home         = "/var/lib/orama-global/chain"
	ServiceUser  = "orama-chain"
	Unit         = "orama-global-chain.service"
	ValidatorKey = "validator"
	RPC          = "tcp://127.0.0.1:31001"
	RPCHTTP      = "http://127.0.0.1:31001"
	P2PPort      = 31000
	RPCPort      = 31001
	GRPCPort     = 31002
	APIPort      = 31003
	PromPort     = 31004
	Denom        = "norama"
	// NoramaPerOrama is chain/app/params.NoramaPerOrama (9 decimals).
	NoramaPerOrama = 1_000_000_000
	// DevnetMarker must be in every chain id this package talks to.
	DevnetMarker = "-devnet-"
)

// Budgets.
const (
	// QueryBudget bounds one remote query.
	QueryBudget = 2 * time.Minute
	// TxBudget bounds one sign+broadcast+inclusion, lock wait included: the
	// chain packages of a stage share three validator keys.
	TxBudget = 10 * time.Minute
	// InclusionTimeout is what `oramad q wait-tx` waits for a block.
	InclusionTimeout = "90s"
	// PollEvery paces waits on chain state.
	PollEvery = 3 * time.Second
	// EpochBudget is several e2e epochs (E2E_EPOCH_DURATION default 60s,
	// e2e/harness/provision/config.go), so a wait for the next close is safe.
	EpochBudget = 6 * time.Minute
)

// Chain is the run's chain as one test sees it.
type Chain struct {
	F  *fleet.Fleet
	ID string
}

// New returns the run's chain, skipping (not covered) when the run has none,
// and refusing a chain id that is not a devnet id.
func New(t testing.TB) *Chain {
	t.Helper()
	harness.RequireChain(t)
	f := harness.Fleet(t)
	id := f.State.ChainID
	if !strings.Contains(id, DevnetMarker) {
		t.Fatalf("refusing chain %q: every run chain is a devnet chain (id contains %s)", id, DevnetMarker)
	}
	return &Chain{F: f, ID: id}
}

// Nodes are the co-hosted validators, node-1..node-3.
func (c *Chain) Nodes() []fleet.Node { return c.F.State.Nodes }

// Node returns the i-th validator node (0-based).
func (c *Chain) Node(t testing.TB, i int) fleet.Node {
	t.Helper()
	if i < 0 || i >= len(c.F.State.Nodes) {
		t.Fatalf("no validator node %d: the run has %d", i, len(c.F.State.Nodes))
	}
	return c.F.State.Nodes[i]
}

// OramadCmd is the shell form of `oramad <args>` run as the chain user with
// the chain home, every argument quoted.
func OramadCmd(args ...string) string {
	q := make([]string, 0, len(args)+6)
	q = append(q, "sudo", "-u", ServiceUser, Oramad)
	for _, a := range args {
		q = append(q, fleet.ShellQuote(a))
	}
	q = append(q, "--home", Home)
	return strings.Join(q, " ")
}

// Run runs a shell command on n with its own budget (fleet.Exec caps at two
// minutes; a transaction may wait on another package's lock) and fails the
// test when it could not run.
func (c *Chain) Run(t testing.TB, n fleet.Node, budget time.Duration, cmd string) fleet.Output {
	t.Helper()
	out, err := c.run(t, n, budget, cmd)
	if err != nil {
		t.Fatal(c.F.Redact(fmt.Sprintf("failed to run on %s: %v", n.Name, err)))
	}
	return out
}

// run is Run returning the error. Its context is its own budget, not the
// test's: cleanups (which run after the test's context is cancelled) use the
// same helpers.
func (c *Chain) run(t testing.TB, n fleet.Node, budget time.Duration, cmd string) (fleet.Output, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	return c.F.SSHFor(t, n).Run(ctx, cmd)
}

// cleanupRun runs cmd from a t.Cleanup, on a context of its own (the test's
// is cancelled by then).
func (c *Chain) cleanupRun(t testing.TB, n fleet.Node, cmd string) (fleet.Output, error) {
	return c.run(t, n, fleet.CleanupBudget, cmd)
}

// CleanupExec runs cmd from a t.Cleanup and reports (never Fatal) a failure.
func (c *Chain) CleanupExec(t testing.TB, n fleet.Node, cmd string) {
	t.Helper()
	out, err := c.cleanupRun(t, n, cmd)
	if err != nil || out.Exit != 0 {
		t.Errorf("cleanup on %s failed (exit %d): %v %s", n.Name, out.Exit, err, c.F.Redact(out.Stderr))
	}
}

// QueryOut runs `oramad query <args> --output json` on n against its
// loopback RPC and returns the raw output, whatever the exit code.
func (c *Chain) QueryOut(t testing.TB, n fleet.Node, args ...string) fleet.Output {
	t.Helper()
	full := append(append([]string{"query"}, args...), "--node", RPC, "--output", "json")
	return c.Run(t, n, QueryBudget, OramadCmd(full...))
}

// Query decodes `oramad query <args>` into v and fails the test on any error.
func (c *Chain) Query(t testing.TB, n fleet.Node, v any, args ...string) {
	t.Helper()
	out := c.QueryOut(t, n, args...)
	if out.Exit != 0 {
		t.Fatal(c.F.Redact(fmt.Sprintf("%s: oramad query %s exited %d: %s", n.Name, strings.Join(args, " "), out.Exit, out.Stderr)))
	}
	if err := json.Unmarshal([]byte(out.Stdout), v); err != nil {
		t.Fatalf("%s: oramad query %s: %v: %s", n.Name, strings.Join(args, " "), err, out.Stdout)
	}
}

// QueryAt is Query at a past height (every oramad query takes --height).
func (c *Chain) QueryAt(t testing.TB, n fleet.Node, height int64, v any, args ...string) {
	t.Helper()
	c.Query(t, n, v, append(args, "--height", fmt.Sprint(height))...)
}

// QueryFails runs a query that must fail and returns its combined output.
func (c *Chain) QueryFails(t testing.TB, n fleet.Node, args ...string) string {
	t.Helper()
	out := c.QueryOut(t, n, args...)
	if out.Exit == 0 {
		t.Fatalf("%s: oramad query %s succeeded, want a refusal: %s", n.Name, strings.Join(args, " "), out.Stdout)
	}
	return out.Stdout + out.Stderr
}

// NotFound reports whether a failed query's output is a gRPC NotFound (the
// CLI prints "rpc error: code = NotFound desc = ...") or says "not found".
func NotFound(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "code = notfound") || strings.Contains(lower, "not found")
}
