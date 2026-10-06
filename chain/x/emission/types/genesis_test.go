package types_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// validEpoch1Ceiling is epoch 1's ceiling record with every amount hard-coded (matching
// TestSplitEpochMint_epoch1TotalSplitsExactly), used as a valid fixture by tests below that are
// really about something else (duplicate epochs, negative amounts, window bounds).
func validEpoch1Ceiling() types.CeilingRecord {
	return types.CeilingRecord{
		Epoch:              1,
		StorageCeiling:     math.NewInt(3_712_000_000_000),
		RelayCeiling:       math.NewInt(1_484_800_000_000),
		DevelopmentCeiling: math.NewInt(742_400_000_000),
		ValidatorMinted:    math.NewInt(8_908_800_000_000),
	}
}

// genesisAfterEpoch1 returns a genesis state as if exactly epoch 1 had closed: current_epoch=2,
// cumulative_minted is epoch 1's hard-coded validator share, and epoch 1's ceiling record present.
func genesisAfterEpoch1() *types.GenesisState {
	gs := types.DefaultGenesisState()
	gs.EpochState.CurrentEpoch = 2
	gs.EpochState.CumulativeMinted = math.NewInt(8_908_800_000_000)
	gs.Ceilings = []types.CeilingRecord{validEpoch1Ceiling()}
	return gs
}

func TestGenesisState_defaultIsValid(t *testing.T) {
	require.NoError(t, types.DefaultGenesisState().Validate())
}

func TestGenesisState_afterEpoch1IsValid(t *testing.T) {
	require.NoError(t, genesisAfterEpoch1().Validate())
}

func TestParams_Validate_rejectsZeroOrNegative(t *testing.T) {
	cases := []struct {
		name   string
		params types.Params
	}{
		{"zero epoch duration", types.NewParams(0, types.MinBlocksFloor, false)},
		{"negative epoch duration", types.NewParams(-time.Hour, types.MinBlocksFloor, false)},
		{"zero min blocks per epoch", types.NewParams(types.EpochDurationFloor, 0, false)},
		{"both zero", types.NewParams(0, 0, false)},
		{"epoch duration over the 365-day cap", types.NewParams(types.MaxEpochDuration+time.Second, types.MinBlocksFloor, false)},
		{"min blocks over the 1e9 cap", types.NewParams(types.EpochDurationFloor, types.MaxBlocksPerEpoch+1, false)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.params.Validate())
		})
	}
}

func TestParams_Validate_belowProductionFloorsRejectedUnlessBootstrapStakeAllowed(t *testing.T) {
	shortDuration := types.NewParams(time.Second, types.MinBlocksFloor, false)
	require.Error(t, shortDuration.Validate(), "epoch_duration below 24h must be rejected in production mode")

	fewBlocks := types.NewParams(types.EpochDurationFloor, 5, false)
	require.Error(t, fewBlocks.Validate(), "min_blocks_per_epoch below 14,400 must be rejected in production mode")

	// The same two values are fine once allow_bootstrap_stake is set.
	shortDurationDevnet := types.NewParams(time.Second, 5, true)
	require.NoError(t, shortDurationDevnet.Validate())
}

func TestParams_Validate_acceptsProductionFloors(t *testing.T) {
	require.NoError(t, types.NewParams(types.EpochDurationFloor, types.MinBlocksFloor, false).Validate())
	require.NoError(t, types.DefaultParams().Validate())
}

func TestGenesisState_Validate_rejectsZeroCurrentEpoch(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.EpochState.CurrentEpoch = 0
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsCurrentEpochAboveMaxSafeEpoch(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.EpochState.CurrentEpoch = types.MaxSafeEpoch + 1
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsNegativeEpochStartUnixNano(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.EpochState.EpochStartUnixNano = -1
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsNegativeCumulativeMinted(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.EpochState.CumulativeMinted = math.NewInt(-1)
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsMintedAboveExactSchedule(t *testing.T) {
	gs := genesisAfterEpoch1()
	gs.EpochState.CumulativeMinted = gs.EpochState.CumulativeMinted.AddRaw(1)
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsMintedBelowExactSchedule(t *testing.T) {
	// The invariant is now exact equality, not just an upper bound: minting less than the
	// schedule's validator share for a closed epoch is just as invalid as minting more, since
	// CloseEpoch always mints the full share unconditionally.
	gs := genesisAfterEpoch1()
	gs.EpochState.CumulativeMinted = gs.EpochState.CumulativeMinted.SubRaw(1)
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsDuplicateCeilingEpoch(t *testing.T) {
	gs := genesisAfterEpoch1()
	gs.Ceilings = []types.CeilingRecord{validEpoch1Ceiling(), validEpoch1Ceiling()}
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsCeilingAmountNotMatchingSchedule(t *testing.T) {
	gs := genesisAfterEpoch1()
	bad := validEpoch1Ceiling()
	bad.StorageCeiling = bad.StorageCeiling.AddRaw(1)
	gs.Ceilings = []types.CeilingRecord{bad}
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsCeilingForUnclosedEpoch(t *testing.T) {
	gs := types.DefaultGenesisState() // current_epoch == 1: no epoch has closed yet
	gs.Ceilings = []types.CeilingRecord{validEpoch1Ceiling()}
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsCeilingOutsideTrailingWindow(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.EpochState.CurrentEpoch = types.CeilingWindow + 2 // completed epochs = CeilingWindow + 1
	gs.EpochState.CumulativeMinted = types.CumulativeValidatorMinted(types.CeilingWindow + 1)
	// Epoch 1 is exactly CeilingWindow+1 - 1 = CeilingWindow epochs old: outside the window.
	bad := validEpoch1Ceiling()
	gs.Ceilings = []types.CeilingRecord{bad}
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsNilCeilingAmount(t *testing.T) {
	gs := genesisAfterEpoch1()
	bad := validEpoch1Ceiling()
	bad.StorageCeiling = math.Int{}
	gs.Ceilings = []types.CeilingRecord{bad}
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsDevelopmentMintAboveCeiling(t *testing.T) {
	gs := genesisAfterEpoch1()
	gs.Ceilings[0].DevelopmentMinted = gs.Ceilings[0].DevelopmentCeiling.AddRaw(1)
	gs.EpochState.CumulativeDevelopmentMinted = gs.Ceilings[0].DevelopmentMinted
	require.Error(t, gs.Validate())
}

func TestGenesisState_Validate_rejectsCumulativeDevelopmentBelowRecords(t *testing.T) {
	gs := genesisAfterEpoch1()
	gs.Ceilings[0].DevelopmentMinted = math.NewInt(10)
	gs.EpochState.CumulativeDevelopmentMinted = math.NewInt(9)
	require.Error(t, gs.Validate())
}

func TestParamsValidate_rejectsDurationThatWouldOverflow(t *testing.T) {
	for _, secs := range []int64{9_300_000_000, 18_446_744_074} {
		p := types.Params{EpochDurationSeconds: secs, MinBlocksPerEpoch: 1, AllowBootstrapStake: true}
		require.Error(t, p.Validate(), "epoch_duration_seconds=%d must be rejected", secs)
	}
}
