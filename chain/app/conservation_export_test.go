package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// The refusal names the way out, and the explicit opt-in exports the same state: export is the recovery
// path after a halt, so a state that breaks conservation must be exportable when an operator asks.
func TestConservation_exportWithTheFlagExportsAViolatingState(t *testing.T) {
	c := newConservationChain(t, 1)
	c.damage(func(ctx sdk.Context) {
		require.NoError(t, c.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, oneNorama()))
	})
	_, err := c.app.ExportAppStateAndValidatorsOpts(false, nil, nil, false)
	require.ErrorContains(t, err, "refusing to export a state that breaks conservation")
	require.ErrorContains(t, err, "--"+app.FlagAllowConservationViolation)

	exported, err := c.app.ExportAppStateAndValidatorsOpts(false, nil, nil, true)
	require.NoError(t, err)
	require.NotEmpty(t, exported.AppState)
	require.Equal(t, c.app.LastBlockHeight()+1, exported.Height)
}

// The flag changes nothing on a clean state.
func TestConservation_exportWithTheFlagOnACleanState(t *testing.T) {
	c := newConservationChain(t, 1)
	require.NoError(t, c.nextBlock())
	exported, err := c.app.ExportAppStateAndValidatorsOpts(false, nil, nil, true)
	require.NoError(t, err)
	require.NotEmpty(t, exported.AppState)
}
