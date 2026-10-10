package types

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"
)

func TestValidate_checksServiceMints(t *testing.T) {
	gs := DefaultGenesisState()
	require.NoError(t, gs.Validate())

	gs.EpochState.CumulativeServiceMinted = math.NewInt(-1)
	require.ErrorContains(t, gs.Validate(), "cumulative_service_minted")

	gs = DefaultGenesisState()
	split := SplitEpochMint(MaxMintableForEpoch(1))
	gs.EpochState.CurrentEpoch = 2
	gs.EpochState.CumulativeMinted = split.Validator
	gs.Ceilings = []CeilingRecord{{
		Epoch: 1, StorageCeiling: split.Storage, RelayCeiling: split.Relay,
		DevelopmentCeiling: split.Development, ValidatorMinted: split.Validator,
		DevelopmentMinted: math.ZeroInt(), RelayMinted: math.ZeroInt(),
		StorageMinted: split.Storage.AddRaw(1),
	}}
	require.ErrorContains(t, gs.Validate(), "storage_minted")

	gs.Ceilings[0].StorageMinted = math.NewInt(5)
	require.ErrorContains(t, gs.Validate(), "cumulative_service_minted", "records sum above the cumulative")
	gs.EpochState.CumulativeServiceMinted = math.NewInt(5)
	require.NoError(t, gs.Validate())
}
