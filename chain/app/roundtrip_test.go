package app_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"
)

// A chain restarted from its own export is the same chain: after the import and the same next block, the
// exported state of the restarted app equals the exported state of the app that never stopped, and
// both pass the full conservation pass. The state comes from the randomized simulation, so it holds
// tokens with every capability, delegations, faucet drips and burned fees, not an empty genesis.
func TestApp_exportImportReproducesTheStateAfterTheNextBlock(t *testing.T) {
	c := newSimChain(t, *simSeed+3000, false, *simCadence)
	for i := 0; i < *simBlocks; i++ {
		c.block()
	}

	exported, err := c.app.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)

	restored := buildTestApp(t)
	_, err = restored.InitChain(&abci.RequestInitChain{
		ChainId:         testChainID,
		InitialHeight:   exported.Height,
		Time:            c.at(c.height),
		AppStateBytes:   exported.AppState,
		ConsensusParams: &exported.ConsensusParams,
	})
	require.NoError(t, err, "the exported state is accepted as a genesis")
	require.Equal(t, c.height+1, exported.Height)

	next := c.at(c.height + 1)
	for _, a := range []interface {
		FinalizeBlock(*abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error)
	}{c.app, restored} {
		_, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: exported.Height, Time: next})
		require.NoError(t, err)
	}
	_, err = c.app.Commit()
	require.NoError(t, err)
	_, err = restored.Commit()
	require.NoError(t, err)

	require.NoError(t, restored.CheckConservationFull(restored.NewContext(true)))
	want, err := c.app.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
	got, err := restored.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
	require.JSONEq(t, string(want.AppState), string(got.AppState))
	require.Equal(t, want.Validators, got.Validators)
}
