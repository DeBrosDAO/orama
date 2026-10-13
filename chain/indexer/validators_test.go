package indexer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"

	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"

	"github.com/DeBrosOfficial/network/chain/app"
)

func TestValidators_signingIsTalliedPerEpoch(t *testing.T) {
	chain, vals := econChain(t)
	addEconBlocks(t, chain, 1, 6)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	third, found, err := store.ValidatorEpochs(vals[2].operator, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, third, 2)
	// Block 1 carries no commit; blocks 2 and 3 are epoch 1's, blocks 4 to 6 epoch 2's. The third
	// validator votes through block 4 and then stops.
	require.Equal(t, uint64(1), third[1].Epoch)
	require.Equal(t, []uint64{2, 0, 10000}, []uint64{third[1].SignedBlocks, third[1].MissedBlocks, third[1].UptimeBps})
	require.Equal(t, uint64(2), third[0].Epoch, "newest first")
	require.Equal(t, []uint64{1, 2, 3333}, []uint64{third[0].SignedBlocks, third[0].MissedBlocks, third[0].UptimeBps})
	require.Equal(t, int64(100), third[0].CometPower)

	first, _, err := store.ValidatorEpochs(vals[0].operator, 1, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(3), first[0].SignedBlocks)
	require.Equal(t, 1, chain.valsetCalls, "a validator set that does not change is read once")
}

func TestValidators_aChangedSetIsReadAgain(t *testing.T) {
	chain, vals := econChain(t)
	addEconBlocks(t, chain, 1, 4)
	// The set shrinks to two members from height 4, so block 5 carries two votes.
	chain.valsetChanges = map[int64][][]byte{4: chain.valset[:2]}
	chain.ledger.validators = vals[:2]
	chain.addBlock(votes(chain.valset[:2], true, true), nil)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()
	require.Equal(t, 2, chain.valsetCalls, "once for the three-member set, once after it changed")
}

func TestValidators_aVoteByTheWrongValidatorIsRefused(t *testing.T) {
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 2)
	chain.blocks[1].commit[0].ValidatorAddress = chain.valset[1]
	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	_, err = f.Step(t.Context())
	require.ErrorContains(t, err, "vote 0 of block 2")
}

func slashEvent(consAddr, reason, jailed string) abci.Event {
	attrs := []abci.EventAttribute{
		{Key: "address", Value: consAddr}, {Key: "power", Value: "100"}, {Key: "reason", Value: reason},
		{Key: "burned_coins", Value: "10"},
	}
	if jailed != "" {
		attrs = append(attrs, abci.EventAttribute{Key: "jailed", Value: jailed})
	}
	return abci.Event{Type: "slash", Attributes: attrs}
}

func TestValidators_slashesAndJailPeriods(t *testing.T) {
	chain, vals := econChain(t)
	third, second := consOf(t, vals[2]), consOf(t, vals[1])
	unjail := okTx(t, one(&slashingtypes.MsgUnjail{ValidatorAddr: vals[2].operator}), one(&slashingtypes.MsgUnjailResponse{}), addr(t, 5))
	chain.addBlock(votes(chain.valset, true, true, true), nil)
	chain.addBlock(votes(chain.valset, true, true, true), nil)
	chain.addBlock(votes(chain.valset, true, true, true), rewardEvents(t))
	// Downtime: one event slashes and jails. Double signing: a slash event and then a jail event.
	chain.addBlock(votes(chain.valset, true, true, true), []abci.Event{slashEvent(third, "missing_signature", third)})
	chain.addBlock(votes(chain.valset, true, true, false), []abci.Event{
		slashEvent(second, "double_sign", ""), {Type: "slash", Attributes: []abci.EventAttribute{{Key: "jailed", Value: second}}},
	})
	chain.addBlock(votes(chain.valset, true, true, false), rewardEvents(t), unjail)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	slashes, found, err := store.ValidatorSlashes(vals[2].operator, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, slashes, 1)
	require.Equal(t, Slash{Height: 4, Time: blockTime(4).UTC(), Epoch: 2, Reason: "missing_signature", Power: 100, Burned: "10"}, slashes[0])

	jails, _, err := store.ValidatorJails(vals[2].operator, 1, 10)
	require.NoError(t, err)
	require.Len(t, jails, 1)
	require.Equal(t, int64(4), jails[0].JailedHeight)
	require.Equal(t, "missing_signature", jails[0].Reason)
	require.Equal(t, int64(6), jails[0].UnjailedHeight, "a successful unjail transaction closes the period")

	jails, _, err = store.ValidatorJails(vals[1].operator, 1, 10)
	require.NoError(t, err)
	require.Len(t, jails, 1)
	require.Equal(t, "double_sign", jails[0].Reason, "the jailing takes the reason of the slash before it")
	require.Zero(t, jails[0].UnjailedHeight, "still jailed")

	rows, _, err := store.ValidatorEpochs(vals[2].operator, 1, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), rows[0].Slashes)
	require.Equal(t, "10", rows[0].SlashedBurned)
	require.Zero(t, rows[1].Slashes)
}

func consOf(t *testing.T, v fakeValidator) string {
	t.Helper()
	s, err := consBech32(v.cons())
	require.NoError(t, err)
	return s
}

func TestValidators_jailedBeforeTheIndexBeganHasNoJailHeight(t *testing.T) {
	chain, vals := econChain(t)
	vals[0].jailed = true
	chain.ledger.validators = vals
	addEconBlocks(t, chain, 1, 4)
	store, _ := follow(t, chain, t.TempDir(), 3)
	defer store.Close()

	jails, found, err := store.ValidatorJails(vals[0].operator, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, jails, 1)
	require.Zero(t, jails[0].JailedHeight, "when it was jailed is not known")
	require.Nil(t, jails[0].JailedTime)

	none, _, err := store.ValidatorJails(vals[1].operator, 1, 10)
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestValidators_aJailingInTheFirstIndexedBlockKnowsItsHeight(t *testing.T) {
	chain, vals := econChain(t)
	vals[0].jailed = true
	chain.ledger.validators = vals
	first := consOf(t, vals[0])
	addEconBlocks(t, chain, 1, 2)
	chain.addBlock(votes(chain.valset, true, true, true), append(rewardEvents(t), slashEvent(first, "missing_signature", first)))
	store, _ := follow(t, chain, t.TempDir(), 3)
	defer store.Close()

	jails, _, err := store.ValidatorJails(vals[0].operator, 1, 10)
	require.NoError(t, err)
	require.Len(t, jails, 1, "the state after the block and the event describe one jailing")
	require.Equal(t, int64(3), jails[0].JailedHeight)
	require.Equal(t, "missing_signature", jails[0].Reason)
}

func TestValidators_aValidatorThatLeftIsReadFromTheRegistry(t *testing.T) {
	chain, vals := econChain(t)
	addEconBlocks(t, chain, 1, 1)
	store, f := follow(t, chain, t.TempDir(), 1)
	defer store.Close()
	w := store.newWriter()
	defer w.close()

	info, err := f.departedValidator(w, vals[0].cons())
	require.NoError(t, err)
	require.Equal(t, vals[0].operator, info.Operator)
	require.Equal(t, StatusUnbonded, info.Status)
	require.Zero(t, info.CometPower)

	stranger := make([]byte, consLen)
	stranger[0] = 7
	info, err = f.departedValidator(w, stranger)
	require.NoError(t, err)
	require.Empty(t, info.Operator, "a validator the index never saw has only its consensus address")
	require.Contains(t, info.ConsensusAddress, "valcons1")
}

func TestValidators_moreThanOnePageOfStakingValidators(t *testing.T) {
	chain, _ := econChain(t)
	var many []fakeValidator
	for i := 0; i < 2*validatorPageSize+50; i++ {
		many = append(many, newFakeValidator(t, byte(i%250), int64(1000-i)))
		many[i].operator = fmt.Sprintf("%s%03d", many[i].operator[:len(many[i].operator)-3], i)
	}
	chain.ledger.validators = many
	chain.valset = nil
	for h := 0; h < 3; h++ {
		chain.addBlock(nil, nil)
	}
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	third, found, err := store.EpochValidators(1, 3, validatorPageSize)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, third, 50)
	require.Equal(t, int64(1000-2*validatorPageSize), third[0].CometPower, "the third page continues the power order")
}

// The module accounts the supply breakdown reads are the app's own.
func TestSupply_theProtocolModulesAreTheAppsModuleAccounts(t *testing.T) {
	want := map[string]bool{}
	for name := range app.GetMaccPerms() {
		want[name] = true
	}
	got := map[string]bool{}
	for _, name := range protocolModules {
		got[name] = true
	}
	require.Equal(t, want, got)
}
