//go:build cgo && orchardffi

package app_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// shieldedInclusionChain is the committee chain of the inclusion tests on an app that has the real
// verifiers, so the proposal proof check is active on it.
func shieldedInclusionChain(t *testing.T) *inclusionChain {
	t.Helper()
	app.SetAddressPrefixes()
	path := realVerifierPath(t)
	pin, err := app.FileSHA256(path)
	require.NoError(t, err)
	setVerifierPin(t, pin)
	a := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true,
		simtestutil.AppOptionsMap{flags.FlagHome: t.TempDir(), app.FlagShieldedVerifier: path}, baseapp.SetChainID(testChainID))
	t.Cleanup(func() { _ = a.Close() })
	mempoolVerifierReady(t, a)
	return newInclusionChainOn(t, a, inclusionEnableHeight)
}

func (c *inclusionChain) shieldTx(member int, seed int64, bundle []byte) []byte {
	c.t.Helper()
	signer := sdk.AccAddress(c.keys[member].account.PubKey().Address()).String()
	return signedTx(c.t, c.app, c.keys[member].account, seed, 2_000_000, &shieldedtypes.MsgShield{Signer: signer, Bundle: bundle})
}

// A transaction an inclusion list forces into the block is not the proposal check's to refuse: the
// inclusion rules require it whatever its proof, and one validator that lists a bad transaction must
// not be able to stall every round. The forced prefix goes to execution, where the verifiers fail the
// transaction with an ordinary result and halt nobody. The proposer's own part of the block is
// gated all the same.
func TestInclusion_aForcedShieldedTxWithABadBundleIsNotRefusedByTheProposalCheck(t *testing.T) {
	cases := []string{"two signed bundles that spend the same nullifier", "one bundle with an invalid proof"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			exited := -1
			old := app.VerifierHaltExit
			app.VerifierHaltExit = func(code int) { exited = code }
			t.Cleanup(func() { app.VerifierHaltExit = old })

			c := shieldedInclusionChain(t)
			anchor := [32]byte{}
			var forced [][]byte
			if name == "one bundle with an invalid proof" {
				forced = append(forced, c.shieldTx(1, 1, testutil.Bundle{Seed: 1, ValueBalance: -1000, Anchor: anchor}.Encode(1)))
			} else {
				// The same note spent by two bundles: one nullifier twice in the forced prefix.
				forced = append(forced, c.shieldTx(1, 1, testutil.Bundle{Seed: 2, ValueBalance: -1000, Anchor: anchor}.Encode(1)))
				forced = append(forced, c.shieldTx(2, 2, testutil.Bundle{Seed: 2, ValueBalance: -1001, Anchor: anchor}.Encode(1)))
			}
			for _, tx := range forced {
				c.app.SeedInclusionPoolForTest(tx)
			}
			c.clock = c.clock.Add(app.InclusionIncludeAfter + 1)
			c.finalize()
			listed := c.extend()
			require.NotEmpty(t, listed, "the starved transactions are listed")
			ec := c.extendedCommit([][]byte{listed, nil, nil}) // one validator lists them

			block := c.prepare(ec)
			require.Len(t, block, 1+len(forced), "the injected commit and every forced transaction, bad proofs and all")
			require.Equal(t, forced, block[1:])
			require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(ec, block), "the forced prefix is not gated")

			// The proposer's own bundle is gated all the same: it is left out, and a block with it is refused.
			own := c.shieldTx(0, 3, testutil.Bundle{Seed: 9, ValueBalance: -1000, Anchor: anchor}.Encode(1))
			require.Equal(t, block, c.prepare(ec, own), "the proposer's own bad bundle is left out")
			require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, append(append([][]byte{}, block...), own)))

			// Execution fails the bad transactions with ordinary results and halts nobody.
			resp := c.finalize(block...)
			require.Len(t, resp.TxResults, len(block))
			for i := 1; i < len(block); i++ {
				require.NotZero(t, resp.TxResults[i].Code, "forced tx %d must fail its verification, not succeed", i)
			}
			require.Equal(t, -1, exited, "no halt: the verifier answered with a verdict")
		})
	}
}
