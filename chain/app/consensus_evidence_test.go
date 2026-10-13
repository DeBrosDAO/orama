package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
)

const evidenceBlockInterval = params.BlockIntervalSeconds * time.Second

// CometBFT verifies evidence against the blocks and validator sets a node still holds, and a
// validator that unbonded before its evidence expired could not be slashed. So the evidence window
// may not exceed what nodes retain, and the retention stays under the unbonding time. The three
// numbers are derived from one another and cannot drift apart.
func TestConsensusEvidenceWindow_isWithinRetentionAndUnbonding(t *testing.T) {
	maxAge := time.Duration(app.ConsensusEvidenceMaxAgeDurationNanos)
	retention := time.Duration(archivetypes.DefaultBlocksIn14Days) * evidenceBlockInterval
	unbonding := stakingUnbondingTime(t)

	require.Equal(t, archivetypes.RetentionDays*24*int64(time.Hour), int64(maxAge), "the evidence window is 14 days")
	require.LessOrEqual(t, app.ConsensusEvidenceMaxAgeNumBlocks, archivetypes.DefaultBlocksIn14Days,
		"evidence older than the retained blocks cannot be verified")
	require.LessOrEqual(t, maxAge, retention)
	require.Less(t, retention, unbonding, "retention must stay under the unbonding time")
	require.Less(t, maxAge, unbonding, "CometBFT requires evidence max age under the unbonding time")
}

func TestConsensusEvidenceWindow_durationIsTheBlockCountAtTheBlockInterval(t *testing.T) {
	require.Equal(t, app.ConsensusEvidenceMaxAgeNumBlocks*int64(evidenceBlockInterval), app.ConsensusEvidenceMaxAgeDurationNanos)
}

// stakingUnbondingTime reads the unbonding time the app's genesis defaults carry.
func stakingUnbondingTime(t *testing.T) time.Duration {
	t.Helper()
	app.SetAddressPrefixes()
	oramaApp := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID(productionChainID))
	t.Cleanup(func() { require.NoError(t, oramaApp.Close()) })
	var gs stakingtypes.GenesisState
	require.NoError(t, oramaApp.AppCodec().UnmarshalJSON(oramaApp.DefaultGenesis()[stakingtypes.ModuleName], &gs))
	return gs.Params.UnbondingTime
}
