package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// Chain ids that only contain a test-network word are production ids. The faucet mints norama for
// anyone who asks, so a production chain id must never reach it by carrying "-stagenet-" or
// "-devnet-" somewhere inside it.
var lookAlikeProductionChainIDs = []string{
	"orama-1-stagenet-x",
	"orama-1-devnet-x",
	"orama-1-localnet-x",
	"mainnet-stagenet-1",
	"orama-a-b-localnet-1",
	"orama-stagenet-",
}

func TestFaucet_refusedOnChainIDsThatOnlyContainATestMarker(t *testing.T) {
	for _, chainID := range lookAlikeProductionChainIDs {
		f := faucetFixture(t)
		f.Ctx = f.Ctx.WithChainID(chainID)

		_, err := f.faucet(testFaucetRecipient, math.NewInt(1))
		require.ErrorIs(t, err, types.ErrFaucetProduction, chainID)
		require.True(t, f.faucetMinted(t).IsZero(), chainID)
	}
}

func TestFaucet_worksOnExactTestChainIDs(t *testing.T) {
	for _, chainID := range []string{"orama-stagenet-10", "orama-devnet-9", "orama-localnet-1", "orama-devnet-e2e-run7", "orama-pubnet-stagenet-1"} {
		f := faucetFixture(t)
		f.Ctx = f.Ctx.WithChainID(chainID)

		_, err := f.faucet(testFaucetRecipient, math.NewInt(1))
		require.NoError(t, err, chainID)
	}
}

func TestInitGenesis_rejectsFaucetAndBootstrapStakeOnChainIDsThatOnlyContainATestMarker(t *testing.T) {
	for _, chainID := range lookAlikeProductionChainIDs {
		f := newTestFixture(t)
		gs := types.DefaultGenesisState()
		gs.Params.FaucetEnabled = true
		require.ErrorContains(t, f.Keeper.InitGenesis(f.Ctx.WithChainID(chainID), *gs), "faucet_enabled requires a chain-id", chainID)

		g := newTestFixture(t)
		bs := types.DefaultGenesisState()
		bs.Params.AllowBootstrapStake = true
		require.ErrorContains(t, g.Keeper.InitGenesis(g.Ctx.WithChainID(chainID), *bs), "allow_bootstrap_stake requires a chain-id", chainID)
	}
}
