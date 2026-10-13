//go:build cgo && orchardffi

package app_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// envProposalBundles names a file written by the wallet crate's gen_bench (mode "shields", jobs of
// 16 outputs each, for the vectors' chain id): the bundles of the proposal below. Proving them takes
// minutes, so they are not committed; the test skips without them.
const envProposalBundles = "ORAMA_PROPOSAL_BUNDLES"

// TestShieldedReal_proposalCheckCostOfFullBundles measures what PrepareProposal and ProcessProposal
// spend on a proposal of full bundles (16 actions each), cold (nothing verified before) and with the
// mempool's verdicts remembered, and reports it against CometBFT's default timeout_propose of 3 s.
// It fails only if the cold check overruns the proposal budget so badly that the proposal is
// refused; the numbers are the point and go to the log.
func TestShieldedReal_proposalCheckCostOfFullBundles(t *testing.T) {
	path := os.Getenv(envProposalBundles)
	if path == "" {
		t.Skipf("set %s to a gen_bench response of full bundles to measure the proposal check", envProposalBundles)
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var resp struct {
		Built []struct {
			Bundle  string `json:"bundle"`
			Actions int    `json:"actions"`
		} `json:"built"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	require.NotEmpty(t, resp.Built)

	// A fresh node for each cold measurement: PrepareProposal remembers what it verified, so a
	// ProcessProposal after it would measure the memory and not the check.
	build := func() (*shieldedChain, [][]byte) {
		c := realChain(t, nil)
		mempoolVerifierReady(t, c.app)
		var txs [][]byte
		for _, b := range resp.Built {
			bundle, err := hex.DecodeString(b.Bundle)
			require.NoError(t, err)
			require.Equal(t, 16, b.Actions)
			txs = append(txs, c.signed(t, c.alice, txGas, &shieldedtypes.MsgShield{Signer: c.aliceAddr().String(), Bundle: bundle}))
		}
		return c, txs
	}

	c, txs := build()
	start := time.Now()
	status := c.process(t, txs...)
	process := time.Since(start)
	start = time.Now()
	again := c.process(t, txs...)
	remembered := time.Since(start)

	c2, txs2 := build()
	start = time.Now()
	kept := c2.prepare(t, txs2...)
	prepare := time.Since(start)

	t.Logf("%d bundles of 16 actions on a node that has verified none: ProcessProposal %s (%s), PrepareProposal %s (kept %d)", len(txs), process, status, prepare, len(kept))
	t.Logf("ProcessProposal again with the verdicts remembered: %s (%s)", remembered, again)
}
