//go:build e2e_fleet

package chainperf

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/perfbase"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	feature = "chain-perf"
	// EnvChainBaselineFile names a previous run's chain-perf/baseline.json to compare with.
	EnvChainBaselineFile = "E2E_CHAIN_BASELINE_FILE"

	// blockWindow is how many blocks the block interval is measured over.
	blockWindow = 40
	// inclusionTxs is how many public transfers are timed from broadcast to block.
	inclusionTxs = 30
	// checkTxTxs is how many public transfers are broadcast back to back for the CheckTx rate.
	checkTxTxs = 200
	// transferNorama is what each transfer pays the payee.
	transferNorama = 1
	// payerFunds is what the faucet gives the payer: far more than the fees
	// (base fee * DefaultGas, about 0.0006 ORAMA a transaction) of all the transfers.
	payerFunds = 5

	// The upper bounds: generous, they catch a broken-slow chain; the
	// comparison with the previous baseline catches drift.
	blockIntervalBound = 15 * time.Second
	inclusionBound     = 40 * time.Second
	checkTxBound       = 2 * time.Second

	// expectedTimeoutCommit is the timeout_commit the SDK writes into config.toml
	// when oramad init creates it (cosmos-sdk server/util.go), which the
	// documentation states as the chain's block interval.
	expectedTimeoutCommit = "5s"
	blockMetricName       = "chain-block-interval"
	inclusionMetricName   = "chain-transfer-inclusion"
	checkTxMetricName     = "chain-checktx"
)

// recorder is this run's chain baseline; the recording and the comparison
// with the previous baseline are features/internal/perfbase, the code perf uses.
var recorder = perfbase.New(feature, EnvChainBaselineFile, perfbase.GatewayRelease)

// runInfo is what a baseline needs beside its numbers to be read later: the
// shape of the run. The hardware is the fleet's server type, in the run's report.
type runInfo struct {
	ChainID        string `json:"chain_id"`
	Validators     int    `json:"validators"`
	TimeoutCommit  string `json:"timeout_commit"`
	BlockFrom      int64  `json:"block_window_from"`
	BlockTo        int64  `json:"block_window_to"`
	InclusionTxs   int    `json:"inclusion_transactions"`
	CheckTxTxs     int    `json:"checktx_transactions"`
	TransferNorama int    `json:"transfer_norama"`
	Gas            uint64 `json:"gas_limit"`
}

// TestChainPerf_baselines measures the chain on the run's fleet and records
// three metrics: the interval between blocks over blockWindow blocks, the
// time a public transfer takes from broadcast to its block, and the CheckTx
// rate of one node. They run in one test because the transfers are signed
// ahead, in one batch, with consecutive sequences of one key.
func TestChainPerf_baselines(t *testing.T) {
	c := chain.New(t)
	n := c.FaucetNode(t)
	info := runInfo{ChainID: c.ID, Validators: len(c.Nodes()), InclusionTxs: inclusionTxs, CheckTxTxs: checkTxTxs,
		TransferNorama: transferNorama, Gas: chain.DefaultGas, TimeoutCommit: configuredTimeoutCommit(t, c, n)}
	if info.TimeoutCommit != expectedTimeoutCommit {
		t.Errorf("timeout_commit is %q on %s, the documentation states the block interval as %s (the cosmos-sdk default oramad init writes)", info.TimeoutCommit, n.Name, expectedTimeoutCommit)
	}

	info.BlockFrom, info.BlockTo = c.WaitBlocks(t, blockWindow)
	realistic.WriteJSON(t, c.F, feature, "run.json", info)
	recorder.Record(t, c.F, realistic.Summarize(blockMetricName, c.BlockIntervalSamples(t, n, info.BlockFrom, info.BlockTo), nil), blockIntervalBound)

	payer := c.NewFundedKey(t, n, "e2e-chain-perf-payer", chain.Orama(payerFunds))
	payee := c.NewKey(t, n, "e2e-chain-perf-payee")
	start, ok := c.AccountOf(t, n, payer.Address)
	if !ok {
		t.Fatalf("%s has no account after a faucet drip", payer.Address)
	}
	total := inclusionTxs + checkTxTxs
	send := chain.NewMsg("/cosmos.bank.v1beta1.MsgSend", map[string]any{
		"from_address": payer.Address, "to_address": payee.Address,
		"amount": []any{map[string]any{"denom": chain.Denom, "amount": fmt.Sprint(transferNorama)}},
	})
	dir := c.PrepareSigned(t, payer, chain.TxOptions{}, []chain.Msg{send}, total)

	recorder.Record(t, c.F, realistic.Summarize(inclusionMetricName, c.InclusionSamples(t, payer, dir, 0, inclusionTxs), nil), inclusionBound)
	recorder.Record(t, c.F, realistic.Summarize(checkTxMetricName, c.CheckTxSamples(t, payer, dir, inclusionTxs, total), nil), checkTxBound)

	// The burst's transfers sit in the mempool until blocks take them: wait for every
	// one before checking what they paid and leaving the chain clean for the next package.
	c.WaitSequence(t, n, payer.Address, start.Sequence+uint64(total))
	want := chain.NewInt(int64(total) * transferNorama)
	if got := c.Bank(t, n, payee.Address); got.Cmp(want) != 0 {
		t.Errorf("the payee holds %s norama after %d transfers of %d, want %s", got.String(), total, transferNorama, want.String())
	}
	c.RequireInvariants(t, "the chain-perf transfers")
}

// configuredTimeoutCommit reads consensus.timeout_commit from n's config.toml.
func configuredTimeoutCommit(t *testing.T, c *chain.Chain, n fleet.Node) string {
	t.Helper()
	out := c.Run(t, n, chain.QueryBudget, "grep -E '^timeout_commit' "+chain.Home+"/config/config.toml")
	if out.Exit != 0 {
		t.Fatalf("%s: no timeout_commit in config.toml (exit %d): %s", n.Name, out.Exit, out.Stderr)
	}
	_, value, ok := strings.Cut(strings.TrimSpace(out.Stdout), "=")
	if !ok {
		t.Fatalf("%s: unreadable timeout_commit line %q", n.Name, out.Stdout)
	}
	return strings.Trim(strings.TrimSpace(value), `"`)
}
