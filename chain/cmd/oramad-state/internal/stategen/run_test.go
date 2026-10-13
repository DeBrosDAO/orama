package stategen

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	cmtcfg "github.com/cometbft/cometbft/config"
	sm "github.com/cometbft/cometbft/state"
	"github.com/stretchr/testify/require"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

// smallPlan has three steps of each kind.
var smallPlan = Plan{Seed: 11, Nullifiers: 250, Assets: 70, Keys: 120, Caps: Caps{Nullifiers: 100, Assets: 25, Accounts: 25}}

var errStop = errors.New("stop here")

func run(t *testing.T, home string, p Plan) Result {
	t.Helper()
	res, err := Run(Config{Home: home, Plan: p})
	require.NoError(t, err)
	return res
}

func TestRun_writesTheCountsOfThePlan(t *testing.T) {
	home := newTestHome(t)
	res := run(t, home, smallPlan)
	require.Equal(t, int64(smallPlan.Steps()), res.Height, "one committed height per step")
	require.Equal(t, uint64(250), res.Nullifiers)
	require.NotEmpty(t, res.AppHash)
	require.Positive(t, res.Sizes.ApplicationDB)
	require.Positive(t, res.Sizes.NullifierDB)

	a, err := OpenApp(home, "", 0)
	require.NoError(t, err)
	defer a.Close()
	ctx := a.NewContext(true)

	for i := uint64(0); i < smallPlan.Nullifiers; i++ {
		spent, err := a.NullifierStore().Spent(Nullifier(smallPlan.Seed, i), res.Height+1)
		require.NoError(t, err)
		require.True(t, spent, "nullifier %d is recorded", i)
	}
	empty, err := a.NullifierStore().Spent(Nullifier(smallPlan.Seed, smallPlan.Nullifiers), res.Height+1)
	require.NoError(t, err)
	require.False(t, empty, "the nullifier after the last is not")
	require.NoError(t, a.ShieldedKeeper.CheckNullifierStore(ctx), "the database folds to the accumulator in state")

	assets := 0
	require.NoError(t, a.CnftKeeper.Decompressed.Walk(ctx, nil, func(_ []byte, _ cnfttypes.DecompressedAsset) (bool, error) {
		assets++
		return false, nil
	}))
	require.Equal(t, int(smallPlan.Assets), assets)
	next, err := a.CnftKeeper.NextTreeID.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(firstID+assetTrees), next)

	for i := uint64(0); i < smallPlan.Accounts(); i++ {
		require.True(t, a.AccountKeeper.HasAccount(ctx, Account(smallPlan.Seed, i)), "account %d", i)
	}
	require.False(t, a.AccountKeeper.HasAccount(ctx, Account(smallPlan.Seed, smallPlan.Accounts())))
}

func TestRun_genesisStartsTheNodeAfterTheLastBlock(t *testing.T) {
	home := newTestHome(t)
	res := run(t, home, smallPlan)
	genesis, err := genutiltypes.AppGenesisFromFile(genesisPath(home))
	require.NoError(t, err)
	require.Equal(t, res.Height+1, genesis.InitialHeight)
	require.Equal(t, res.AppHash, hex.EncodeToString(genesis.AppHash))
	require.Len(t, genesis.Consensus.Validators, 1, "the committee member is the genesis validator")
	require.True(t, genesis.GenesisTime.After(testGenesisTime), "the first block is later than the generator's")
}

func TestRun_savesCometBFTsStateForTheGenesis(t *testing.T) {
	home := newTestHome(t)
	res := run(t, home, smallPlan)
	cfg, err := LoadCometConfig(home)
	require.NoError(t, err)
	db, err := cmtcfg.DefaultDBProvider(&cmtcfg.DBContext{ID: "state", Config: cfg})
	require.NoError(t, err)
	defer db.Close()
	state, err := sm.NewStore(db, sm.StoreOptions{}).Load()
	require.NoError(t, err)
	require.False(t, state.IsEmpty(), "without a saved state a node stops after its handshake")
	require.Equal(t, res.Height+1, state.InitialHeight)
	require.Equal(t, int64(0), state.LastBlockHeight, "CometBFT has seen no block")
	require.Equal(t, res.AppHash, hex.EncodeToString(state.AppHash))
	require.Equal(t, 1, state.Validators.Size())
}

func TestRun_withoutAConfigTomlTheCometStateCannotBeSaved(t *testing.T) {
	home := newTestHome(t)
	require.NoError(t, os.Remove(filepath.Join(home, configDir, cometConfigFile)))
	_, err := Run(Config{Home: home, Plan: smallPlan})
	require.ErrorContains(t, err, cometConfigFile)
}

func TestRun_isDeterministic(t *testing.T) {
	first, second := run(t, newTestHome(t), smallPlan), run(t, newTestHome(t), smallPlan)
	require.Equal(t, first.AppHash, second.AppHash, "the same plan writes the same state")
	other := smallPlan
	other.Seed++
	require.NotEqual(t, first.AppHash, run(t, newTestHome(t), other).AppHash, "another seed writes another state")
}

func TestRun_resumesAfterAStopWithTheSameResult(t *testing.T) {
	whole := run(t, newTestHome(t), smallPlan)

	home := newTestHome(t)
	_, err := Run(Config{Home: home, Plan: smallPlan, Progress: func(step, _ uint64) error {
		if step == 2 {
			return errStop
		}
		return nil
	}})
	require.ErrorIs(t, err, errStop)

	resumed := run(t, home, smallPlan)
	require.Equal(t, int64(2), resumed.Resumed, "the second run starts after the two committed steps")
	require.Equal(t, whole.AppHash, resumed.AppHash, "a resumed run ends in the state of an uninterrupted one")
	require.Equal(t, whole.Nullifiers, resumed.Nullifiers)
}

func TestRun_aFinishedHomeIsLeftAsItIs(t *testing.T) {
	home := newTestHome(t)
	first := run(t, home, smallPlan)
	again := run(t, home, smallPlan)
	require.Equal(t, first.AppHash, again.AppHash)
	require.Equal(t, first.Height, again.Resumed)
}

func TestRun_refusesAnotherPlanOnAStartedHome(t *testing.T) {
	home := newTestHome(t)
	run(t, home, smallPlan)
	other := smallPlan
	other.Nullifiers++
	_, err := Run(Config{Home: home, Plan: other})
	require.ErrorContains(t, err, "same plan")
}

func TestRun_errors(t *testing.T) {
	_, err := Run(Config{Home: t.TempDir(), Plan: Plan{}})
	require.ErrorContains(t, err, "writes nothing")
	_, err = Run(Config{Home: t.TempDir(), Plan: smallPlan})
	require.Error(t, err, "a directory that is no node home is refused")
}

func TestRun_onlyOneKindOfStateWorks(t *testing.T) {
	for name, p := range map[string]Plan{
		"nullifiers only": {Seed: 1, Nullifiers: 30},
		"assets only":     {Seed: 1, Assets: 30},
		"keys only":       {Seed: 1, Keys: 30},
	} {
		t.Run(name, func(t *testing.T) {
			res := run(t, newTestHome(t), p)
			require.Equal(t, int64(1), res.Height)
			require.Equal(t, p.Nullifiers, res.Nullifiers)
		})
	}
}

// TestKeysPerAccount_matchesTheStore counts the IAVL keys an account adds, so KeysPerAccount is
// what the store holds and not what this file believes.
func TestKeysPerAccount_matchesTheStore(t *testing.T) {
	const accounts = 200
	without, with := newTestHome(t), newTestHome(t)
	run(t, without, Plan{Seed: 1, Nullifiers: 1})
	run(t, with, Plan{Seed: 1, Nullifiers: 1, Keys: accounts * KeysPerAccount})
	require.Equal(t, authKeys(t, without)+accounts*KeysPerAccount, authKeys(t, with))
}

// authKeys counts the keys of the x/auth store of a home.
func authKeys(t *testing.T, home string) int {
	t.Helper()
	a, err := OpenApp(home, "", 0)
	require.NoError(t, err)
	defer a.Close()
	it := a.CommitMultiStore().GetKVStore(a.GetKey(authtypes.StoreKey)).Iterator(nil, nil)
	defer it.Close()
	n := 0
	for ; it.Valid(); it.Next() {
		n++
	}
	return n
}
