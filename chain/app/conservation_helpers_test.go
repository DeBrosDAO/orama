package app_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// testMinBlocksPerEpoch is the emission min_blocks_per_epoch that committeeGenesis writes, and so how
// many blocks apart the chain's own end-of-block full conservation pass runs. One runs it every block;
// a test that wants the production shape (a pass only at some heights) sets it with withCadence.
var testMinBlocksPerEpoch uint64 = 1

// withCadence sets min_blocks_per_epoch for the genesis a test builds next and restores it when the
// test ends. Tests do not run in parallel, so the package variable is not shared.
func withCadence(t testing.TB, minBlocksPerEpoch uint64) {
	t.Helper()
	prev := testMinBlocksPerEpoch
	testMinBlocksPerEpoch = minBlocksPerEpoch
	t.Cleanup(func() { testMinBlocksPerEpoch = prev })
}

// requireConserved runs the full conservation pass over the last committed state. Call it only after a
// finalized block: coins burned by a keeper write are booked with x/emission in the next end-blocker.
func requireConserved(t testing.TB, a *app.OramaApp) {
	t.Helper()
	require.NoError(t, a.CheckConservationFull(a.NewContext(true)))
}

// mintNoramaForTest creates coins from x/emission's module account the way a test funds an account,
// and books the norama among them as supply that existed before the schedule, which is what x/emission
// does with a genesis supply. The chain refuses a block whose norama supply is above what x/emission
// recorded (conservation.go), so a test that mints through the bank keeper alone halts at its next
// block, exactly as a production mint outside x/emission would.
func mintNoramaForTest(a *app.OramaApp, ctx sdk.Context, coins sdk.Coins) error {
	if err := a.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, coins); err != nil {
		return err
	}
	norama := coins.AmountOf(params.BaseDenom)
	if norama.IsZero() {
		return nil
	}
	state, err := a.EmissionKeeper.EpochState.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return nil // an app that never ran InitChain has no ledger and no blocks to halt
	}
	if err != nil {
		return err
	}
	state.GenesisSupply = state.GenesisSupply.Add(norama)
	return a.EmissionKeeper.EpochState.Set(ctx, state)
}
