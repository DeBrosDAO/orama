package indexer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"

	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// epochBlocks is the length of an epoch of the fake chain: blocks are a second apart and both the
// duration and the block count of the closing rule are this long.
const epochBlocks = 3

// econChain is a chain whose epoch closes every epochBlocks blocks and that has three bonded
// validators of voting power 300, 200 and 100. A validator mints 600 norama per epoch, 50 for
// development and 30 for services; a block burns 2 and collects 5 in fees.
func econChain(t *testing.T) (*fakeChain, []fakeValidator) {
	t.Helper()
	chain := newFakeChain()
	l := chain.ledger.epochs(epochBlocks)
	l.mintPerEpoch, l.devPerEpoch, l.svcPerEpoch, l.burnPerBlock, l.feesPerBlock = 600, 50, 30, 2, 5
	vals := []fakeValidator{newFakeValidator(t, 1, 300), newFakeValidator(t, 2, 200), newFakeValidator(t, 3, 100)}
	l.validators = vals
	for _, v := range vals {
		chain.valset = append(chain.valset, v.cons())
	}
	return chain, vals
}

// rewardEvents are the bank transfers x/power makes when an epoch closes: the minted share moves
// from x/emission to x/power, two earnings credits go to x/fees, 40 is force-bonded to a member's
// account and 10 of an unpayable share returns to x/emission.
func rewardEvents(t *testing.T) []abci.Event {
	t.Helper()
	power, err := moduleAddress(powertypes.ModuleName)
	require.NoError(t, err)
	fees, err := moduleAddress(feestypes.ModuleName)
	require.NoError(t, err)
	emission, err := moduleAddress(emissiontypes.ModuleName)
	require.NoError(t, err)
	member := addr(t, 9)
	transfer := func(from, to, amount string) abci.Event {
		return abci.Event{Type: "transfer", Attributes: []abci.EventAttribute{
			{Key: "recipient", Value: to}, {Key: "sender", Value: from}, {Key: "amount", Value: amount + "norama"},
		}}
	}
	return []abci.Event{
		transfer(emission, power, "600"), transfer(power, fees, "100"), transfer(power, fees, "200"),
		transfer(power, member, "40"), transfer(power, emission, "10"),
	}
}

// addEconBlocks adds blocks from..to. Every validator votes in every block except the third, which
// votes in none from block 5 on; block h closes an epoch when h is a multiple of epochBlocks.
func addEconBlocks(t *testing.T, chain *fakeChain, from, to int64) {
	t.Helper()
	for h := from; h <= to; h++ {
		var events []abci.Event
		if h%epochBlocks == 0 {
			events = rewardEvents(t)
		}
		chain.addBlock(votes(chain.valset, true, true, h < 5), events)
	}
}

func follow(t *testing.T, chain *fakeChain, dir string, start int64) (*Store, *Follower) {
	t.Helper()
	store := openStore(t, dir)
	f, err := NewFollower(chain, store, start)
	require.NoError(t, err)
	_, err = f.Step(context.Background())
	require.NoError(t, err)
	return store, f
}

func epochOf(t *testing.T, s *Store, epoch uint64) EpochRow {
	t.Helper()
	row, ok, err := s.Epoch(epoch)
	require.NoError(t, err)
	require.True(t, ok, "epoch %d", epoch)
	return row
}

func TestEpochs_rowsAreTheChainsOwnFigures(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 7)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	rows, err := store.Epochs(1, 10)
	require.NoError(t, err)
	require.Len(t, rows, 2, "epoch 3 is still open")
	require.Equal(t, uint64(2), rows[0].Epoch, "newest first")

	e1 := epochOf(t, store, 1)
	require.Equal(t, int64(1), e1.StartHeight)
	require.Equal(t, int64(3), e1.EndHeight)
	require.Equal(t, int64(3), e1.Blocks)
	require.True(t, e1.Partial, "the first epoch an index tracks measures from its first block")
	require.Equal(t, blockTime(0).UTC(), e1.StartTime, "the epoch start the chain records")
	require.Equal(t, blockTime(3).UTC(), e1.EndTime)
	require.Equal(t, "600", e1.MintedValidators)
	require.Equal(t, "680", e1.MintedTotal)
	require.Equal(t, "4", e1.Burned, "2 per block for the 2 blocks after the baseline block")
	require.Equal(t, "10", e1.FeesCollected)
	require.Equal(t, "1000000", e1.MaxMintable)

	e2 := epochOf(t, store, 2)
	require.Equal(t, int64(4), e2.StartHeight, "an epoch starts the block after the one that closed the last")
	require.Equal(t, blockTime(3).UTC(), e2.StartTime)
	require.False(t, e2.Partial)
	require.Equal(t, "600", e2.MintedValidators)
	require.Equal(t, "50", e2.MintedDevelopment)
	require.Equal(t, "30", e2.MintedService)
	require.Equal(t, "0", e2.MintedFaucet)
	require.Equal(t, "6", e2.Burned)
	require.Equal(t, "15", e2.FeesCollected)
	require.Equal(t, "3", e2.FeesBurned)
	require.Equal(t, "300", e2.RewardsCredited, "only x/power's transfers to x/fees")
	require.Equal(t, "40", e2.RewardsForceBonded, "a transfer to an account, not the returned share")

	_, ok, err := store.Epoch(3)
	require.NoError(t, err)
	require.False(t, ok, "an epoch that has not closed is not in the index")
}

func TestEpochs_supplyPointPerEpoch(t *testing.T) {
	chain, _ := econChain(t)
	l := chain.ledger
	l.balances = map[string]int64{"bonded_tokens_pool": 600, "not_bonded_tokens_pool": 200, "fees": 300, "storage_escrow": 50, "power": 7, "nodes": 3}
	addEconBlocks(t, chain, 1, 6)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	points, err := store.Supply(1, 10)
	require.NoError(t, err)
	require.Len(t, points, 2)
	p := points[0]
	require.Equal(t, uint64(2), p.Epoch)
	require.Equal(t, int64(6), p.Height)
	require.Equal(t, "600", p.Bonded)
	require.Equal(t, "200", p.Unbonding)
	require.Equal(t, "300", p.EarningsPools)
	require.Equal(t, "50", p.StorageEscrow)
	require.Equal(t, "10", p.OtherProtocol, "x/power and x/nodes")
	require.Equal(t, "1360", p.MintedToDate, "two epochs of 600 + 50 + 30")
	require.Equal(t, "12", p.BurnedToDate)
	require.Equal(t, "1348", p.TotalSupply)
	require.Equal(t, "0", p.GenesisSupply)
	require.Equal(t, "188", p.Circulating, "total supply less everything the protocol accounts hold")
}

func TestEpochs_aLaterStartHeightKnowsWhereTheEpochBegan(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 9)
	store, _ := follow(t, chain, t.TempDir(), 5)
	defer store.Close()

	_, ok, err := store.Epoch(1)
	require.NoError(t, err)
	require.False(t, ok, "epoch 1 closed before the index began")

	e2 := epochOf(t, store, 2)
	require.Equal(t, int64(4), e2.StartHeight, "two blocks into the epoch at height 5: it began at 4")
	require.Equal(t, int64(6), e2.EndHeight)
	require.True(t, e2.Partial)
	require.Equal(t, "2", e2.Burned, "only the block after the baseline block 5")
	e3 := epochOf(t, store, 3)
	require.False(t, e3.Partial)
	require.Equal(t, "6", e3.Burned)
}

func TestEpochs_startingOnAClosingBlockMissesNothingOfTheNextEpoch(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 6)
	store, _ := follow(t, chain, t.TempDir(), 3)
	defer store.Close()

	e2 := epochOf(t, store, 2)
	require.Equal(t, int64(4), e2.StartHeight)
	require.False(t, e2.Partial, "block 3 closed epoch 1: the state read after it is epoch 2's start")
	require.Equal(t, "6", e2.Burned, "all three of its blocks")
	require.Equal(t, "15", e2.FeesCollected)
	_, ok, err := store.Epoch(1)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestEpochs_aRestartAndAFullRunAgree(t *testing.T) {
	whole, _ := econChain(t)
	addEconBlocks(t, whole, 1, 9)
	wholeStore, _ := follow(t, whole, t.TempDir(), 1)
	defer wholeStore.Close()

	staged, _ := econChain(t)
	addEconBlocks(t, staged, 1, 4)
	dir := t.TempDir()
	store, _ := follow(t, staged, dir, 1)
	require.NoError(t, store.Close())
	addEconBlocks(t, staged, 5, 9)
	resumed, f := follow(t, staged, dir, 1)
	defer resumed.Close()

	require.Equal(t, dump(t, wholeStore), dump(t, resumed), "stopping in the middle of an epoch changes nothing")
	n, err := f.Step(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestEpochs_foldingABlockAgainChangesNothing(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 6)
	store, f := follow(t, chain, t.TempDir(), 1)
	defer store.Close()
	before := dump(t, store)

	w := store.newWriter()
	err := f.aggregate(context.Background(), w, blockFacts{height: 6, time: blockTime(6), events: rewardEvents(t)})
	require.NoError(t, err)
	require.True(t, w.b.Empty(), "a block the aggregates already hold writes nothing")
	require.NoError(t, w.close())
	require.Equal(t, before, dump(t, store))
}

func TestEpochs_aChainThatDoesNotCloseWhenTheRuleSaysIsRefused(t *testing.T) {
	chain, _ := econChain(t)
	chain.ledger.stuck = true
	chain.ledger.durationSec, chain.ledger.minBlocks = epochBlocks, epochBlocks
	addEconBlocks(t, chain, 1, 4)
	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)

	_, err = f.Step(context.Background())
	require.ErrorContains(t, err, "epoch 1 should have closed at block 3")
	st, err := store.Status()
	require.NoError(t, err)
	require.Equal(t, int64(2), st.Cursor, "the index stops before the block it cannot explain")
}

func TestEpochs_aMissedEpochBoundaryIsRefused(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 4)
	// At block 3 the chain reports epoch 3, begun at block 2: the index would have skipped an epoch.
	chain.queries[queryKey(queryEmissionState, 3)] = &emissiontypes.QueryCurrentEpochResponse{EpochState: stateWith(3, blockTime(2))}
	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)

	_, err = f.Step(context.Background())
	require.ErrorContains(t, err, "an epoch boundary was missed")
}

// dump is every key and value of the index.
func dump(t *testing.T, s *Store) map[string]string {
	t.Helper()
	it, err := s.db.NewIter(nil)
	require.NoError(t, err)
	defer it.Close()
	out := map[string]string{}
	for ok := it.First(); ok; ok = it.Next() {
		out[string(it.Key())] = string(it.Value())
	}
	return out
}

func stateWith(epoch uint64, start time.Time) emissiontypes.EpochState {
	s := newFakeLedger().state(0)
	s.CurrentEpoch, s.EpochStartUnixNano = epoch, start.UnixNano()
	return s
}

func TestEpochs_validatorPowerIsTheSetAtTheClosingHeight(t *testing.T) {
	chain, vals := econChain(t)
	vals[2].status = stakingtypes.Unbonding
	chain.ledger.validators = vals
	addEconBlocks(t, chain, 1, 3)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	rows, found, err := store.EpochValidators(1, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, rows, 3, "bonded at the end, or voting in the epoch")
	require.Equal(t, []int64{300, 200, 0}, []int64{rows[0].CometPower, rows[1].CometPower, rows[2].CometPower}, "highest power first")
	require.Equal(t, StatusUnbonding, rows[2].Status)
}
