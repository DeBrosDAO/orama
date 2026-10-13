package app_test

import (
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtprototypes "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
)

const (
	retainTestMinBlocks = 2
	retainTestWindow    = 3
	retainTestBlocks    = 14
)

// runRetainChain commits retainTestBlocks blocks on an app that prunes (min-retain-blocks) with a
// retention window of retainTestWindow, after setting the archived prefix to lastArchived, and
// returns the retain height of every commit by height.
func runRetainChain(t *testing.T, lastArchived int64) map[int64]int64 {
	t.Helper()
	return runRetainChainWithEvidence(t, lastArchived, retainTestMinBlocks)
}

// runRetainChainWithEvidence is runRetainChain with the evidence age of the consensus parameters
// set to evidenceBlocks.
func runRetainChainWithEvidence(t *testing.T, lastArchived, evidenceBlocks int64) map[int64]int64 {
	t.Helper()
	app.SetAddressPrefixes()
	oramaApp := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{},
		baseapp.SetChainID(testChainID), baseapp.SetMinRetainBlocks(retainTestMinBlocks))
	t.Cleanup(func() { require.NoError(t, oramaApp.Close()) })
	genState, _ := buildGenesisState(t, oramaApp)
	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)
	genesisTime := time.Unix(1_700_000_000, 0)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{
		ChainId: testChainID, InitialHeight: 1, Time: genesisTime, AppStateBytes: stateBytes,
		ConsensusParams: &cmtprototypes.ConsensusParams{
			Block:     &cmtprototypes.BlockParams{MaxBytes: 1 << 20, MaxGas: -1},
			Evidence:  &cmtprototypes.EvidenceParams{MaxAgeNumBlocks: evidenceBlocks, MaxAgeDuration: time.Hour, MaxBytes: 1024},
			Validator: &cmtprototypes.ValidatorParams{PubKeyTypes: []string{"ed25519"}},
		},
	})
	require.NoError(t, err)

	retain := map[int64]int64{}
	commit := func(h int64) {
		_, err = oramaApp.FinalizeBlock(&abci.RequestFinalizeBlock{Height: h, Time: genesisTime.Add(time.Duration(h) * time.Second)})
		require.NoError(t, err)
		resp, err := oramaApp.Commit()
		require.NoError(t, err)
		retain[h] = resp.RetainHeight
	}
	commit(1)

	// The genesis window cannot be shorter than 14 days of blocks, so the test writes a short
	// one straight into the committed store, as x/archive would hold it after genesis.
	ctx := sdk.NewContext(oramaApp.CommitMultiStore(), cmtprototypes.Header{Height: 1}, false, oramaApp.Logger())
	require.NoError(t, oramaApp.ArchiveKeeper.Params.Set(ctx, archivetypes.Params{RetentionWindowBlocks: retainTestWindow}))
	require.NoError(t, oramaApp.ArchiveKeeper.LastArchivedHeight.Set(ctx, lastArchived))
	for h := int64(2); h <= retainTestBlocks; h++ {
		commit(h)
	}
	return retain
}

func TestCommit_retainHeightNeverPassesTheArchivedPrefix(t *testing.T) {
	const archived int64 = 5
	retain := runRetainChain(t, archived)
	for h := int64(1); h <= retainTestBlocks; h++ {
		require.LessOrEqual(t, retain[h], archived, "block %d: retain height %d passes the archived prefix", h, retain[h])
	}
	require.Equal(t, archived, retain[retainTestBlocks],
		"a stalled archive holds the retain height on the last archived block while the tip runs on")
	require.Equal(t, int64(3), retain[6], "below the archived prefix the 14-day window binds: 6 - 3")
}

// The evidence window is the oldest block CometBFT can verify a double sign against, so the retain
// height never passes tip - max_age_num_blocks, even when min-retain-blocks and the archive
// window would let it.
func TestCommit_retainHeightHonoursTheEvidenceWindow(t *testing.T) {
	const evidenceBlocks, archived int64 = 8, 100
	retain := runRetainChainWithEvidence(t, archived, evidenceBlocks)
	for h := evidenceBlocks + 1; h <= retainTestBlocks; h++ {
		require.LessOrEqual(t, retain[h], h-evidenceBlocks, "block %d: retain height %d prunes inside the evidence window", h, retain[h])
	}
	require.Equal(t, retainTestBlocks-evidenceBlocks, retain[retainTestBlocks])
}

func TestCommit_nothingIsPrunedWhileNothingIsArchived(t *testing.T) {
	retain := runRetainChain(t, 0)
	for h := int64(1); h <= retainTestBlocks; h++ {
		require.Zero(t, retain[h], "block %d: pruning must stay off until a range is archived", h)
	}
}

func TestCommit_pruningStaysOffWhenTheOperatorDidNotEnableIt(t *testing.T) {
	app.SetAddressPrefixes()
	oramaApp := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID(testChainID))
	t.Cleanup(func() { require.NoError(t, oramaApp.Close()) })
	genState, _ := buildGenesisState(t, oramaApp)
	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)
	genesisTime := time.Unix(1_700_000_000, 0)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{ChainId: testChainID, InitialHeight: 1, Time: genesisTime, AppStateBytes: stateBytes})
	require.NoError(t, err)
	_, err = oramaApp.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: genesisTime.Add(time.Second)})
	require.NoError(t, err)
	resp, err := oramaApp.Commit()
	require.NoError(t, err)
	require.Zero(t, resp.RetainHeight)
}
