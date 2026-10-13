package indexer

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"

	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
)

func attrs(kv ...string) []abci.EventAttribute {
	out := make([]abci.EventAttribute, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, abci.EventAttribute{Key: kv[i], Value: kv[i+1]})
	}
	return out
}

func validatorRewardEvent(operator, amount, commission, share, delegators, bonded string) abci.Event {
	return abci.Event{Type: powertypes.EventTypeValidatorReward, Attributes: attrs(
		powertypes.AttributeKeyValidator, operator, powertypes.AttributeKeyAmount, amount,
		powertypes.AttributeKeyCommission, commission, powertypes.AttributeKeyOperatorShare, share,
		powertypes.AttributeKeyDelegators, delegators, powertypes.AttributeKeyForceBonded, bonded)}
}

func delegatorRewardEvent(operator, delegator, amount string) abci.Event {
	return abci.Event{Type: powertypes.EventTypeDelegatorReward, Attributes: attrs(
		powertypes.AttributeKeyValidator, operator, powertypes.AttributeKeyDelegator, delegator, powertypes.AttributeKeyAmount, amount)}
}

// paidEvents is a payout whose events and bank transfers agree. The first validator is paid 200
// (commission 20, operator 150, one delegator 50), the second 140 with 40 force-bonded to its
// operator. The delegator is addr(t, 7).
func paidEvents(t *testing.T, vals []fakeValidator) (events []abci.Event, delegator string) {
	t.Helper()
	delegator = addr(t, 7)
	events = append(rewardEvents(t),
		validatorRewardEvent(vals[0].operator, "200", "20", "150", "50", "0"),
		delegatorRewardEvent(vals[0].operator, delegator, "50"),
		validatorRewardEvent(vals[1].operator, "140", "0", "140", "0", "40"),
	)
	return events, delegator
}

// rewardChain closes epochs 1 and 2 at blocks 3 and 6. Epoch 1 closes with the old transfers alone
// when `oldFirst` is set, as a chain did before it reported rewards; the epochs after close with
// the events too.
func rewardChain(t *testing.T, oldFirst bool) (*fakeChain, []fakeValidator, string) {
	t.Helper()
	chain, vals := econChain(t)
	paid, delegator := paidEvents(t, vals)
	for h := int64(1); h <= 7; h++ {
		var events []abci.Event
		switch {
		case h == 3 && oldFirst:
			events = rewardEvents(t)
		case h%epochBlocks == 0:
			events = paid
		}
		chain.addBlock(votes(chain.valset, true, true, true), events)
	}
	return chain, vals, delegator
}

func TestRewards_aPayoutIsRecordedValidatorByValidator(t *testing.T) {
	chain, vals, delegator := rewardChain(t, false)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	e1 := epochOf(t, store, 1)
	require.True(t, e1.RewardsRecorded)
	require.Equal(t, "300", e1.RewardsCredited)
	require.Equal(t, "40", e1.RewardsForceBonded)
	require.Equal(t, "20", e1.RewardsCommission)
	require.Equal(t, "290", e1.RewardsOperatorShare)
	require.Equal(t, "50", e1.RewardsDelegators)

	rows, recorded, found, err := store.EpochRewards(1, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, recorded)
	require.Len(t, rows, 2)
	byOp := map[string]ValidatorReward{}
	for _, r := range rows {
		byOp[r.Operator] = r
	}
	require.Equal(t, ValidatorReward{Epoch: 1, Operator: vals[0].operator, Amount: "200", Commission: "20", OperatorShare: "150", Delegators: "50", ForceBonded: "0"}, byOp[vals[0].operator])
	require.Equal(t, "40", byOp[vals[1].operator].ForceBonded)

	history, found, err := store.ValidatorRewards(vals[0].operator, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, history, 2, "epochs 1 and 2")
	require.Equal(t, uint64(2), history[0].Epoch, "newest first")
	require.True(t, history[0].Recorded)
	require.Equal(t, "200", history[0].Reward.Amount)

	from, credits, err := store.AccountRewards(delegator, 1, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), from)
	require.Len(t, credits, 2)
	require.Equal(t, AccountReward{Epoch: 2, Validator: vals[0].operator, Role: RoleDelegator, Amount: "50", ForceBonded: "0"}, credits[0])

	operatorAcct, err := operatorAccount(vals[1].operator)
	require.NoError(t, err)
	_, own, err := store.AccountRewards(operatorAcct, 1, 10)
	require.NoError(t, err)
	require.Len(t, own, 2)
	require.Equal(t, AccountReward{Epoch: 2, Validator: vals[1].operator, Role: RoleOperator, Amount: "140", ForceBonded: "40"}, own[0])
}

func TestRewards_anEpochBeforeTheChainReportedThemIsNotRecorded(t *testing.T) {
	chain, vals, delegator := rewardChain(t, true)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	e1, e2 := epochOf(t, store, 1), epochOf(t, store, 2)
	require.False(t, e1.RewardsRecorded, "the closing block moved coins and reported no validator")
	require.Empty(t, e1.RewardsCommission)
	require.Equal(t, "300", e1.RewardsCredited, "the transfers' totals are still the chain's")
	require.True(t, e2.RewardsRecorded)

	rows, recorded, found, err := store.EpochRewards(1, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, recorded)
	require.Empty(t, rows)

	history, _, err := store.ValidatorRewards(vals[0].operator, 1, 10)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.True(t, history[0].Recorded)
	require.False(t, history[1].Recorded, "epoch 1 is shown as not recorded, not as nothing paid")
	require.Nil(t, history[1].Reward)

	from, credits, err := store.AccountRewards(delegator, 1, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(2), from, "payouts are known from the first epoch the chain reported")
	require.Len(t, credits, 1)
}

func TestRewards_aValidatorPaidNothingInARecordedEpochHasNoReward(t *testing.T) {
	chain, vals, _ := rewardChain(t, false)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	history, found, err := store.ValidatorRewards(vals[2].operator, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, history, 2)
	for _, h := range history {
		require.True(t, h.Recorded)
		require.Nil(t, h.Reward, "recorded and paid nothing")
	}
}

func TestRewards_anEpochThatPaidNothingIsRecordedAsSuch(t *testing.T) {
	chain, _ := econChain(t)
	for h := int64(1); h <= 4; h++ {
		chain.addBlock(votes(chain.valset, true, true, true), nil)
	}
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()

	e1 := epochOf(t, store, 1)
	require.True(t, e1.RewardsRecorded, "nothing moved, so there is nothing to report")
	require.Equal(t, "0", e1.RewardsOperatorShare)
	_, recorded, _, err := store.EpochRewards(1, 1, 10)
	require.NoError(t, err)
	require.True(t, recorded)
	from, _, err := store.AccountRewards(addr(t, 7), 1, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), from)
}

func TestRewards_anEpochWhoseStateIsPrunedStillHasItsPayout(t *testing.T) {
	chain, vals := econChain(t)
	paid, _ := paidEvents(t, vals)
	add := func(from, to int64) {
		for h := from; h <= to; h++ {
			var events []abci.Event
			if h%epochBlocks == 0 {
				events = paid
			}
			chain.addBlock(votes(chain.valset, true, true, true), events)
		}
	}
	add(1, 4)
	store, f := newPrunedFollower(t, chain, t.TempDir(), 1)
	defer store.Close()
	_, err := f.Step(context.Background())
	require.NoError(t, err)
	add(5, 16)
	chain.prunedBelow = 11
	_, err = f.Step(context.Background())
	require.NoError(t, err)

	e2 := epochOf(t, store, 2)
	require.True(t, e2.Unavailable, "epoch 2 closed at a height whose state is gone")
	require.Empty(t, e2.RewardsCredited, "what the state would say is not made up")
	require.True(t, e2.RewardsRecorded, "the payout is in the closing block's events, which are not state")
	require.Equal(t, "290", e2.RewardsOperatorShare)
	rows, recorded, found, err := store.EpochRewards(2, 1, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, recorded)
	require.Len(t, rows, 2)
}

func TestRewards_eventsThatDisagreeWithTheTransfersStopTheIndex(t *testing.T) {
	cases := map[string]func(vals []fakeValidator, d string) []abci.Event{
		"amounts that differ from what x/power moved": func(vals []fakeValidator, d string) []abci.Event {
			return append(rewardEvents(t), validatorRewardEvent(vals[0].operator, "999", "0", "999", "0", "0"))
		},
		"a split that does not add up": func(vals []fakeValidator, d string) []abci.Event {
			return append(rewardEvents(t), validatorRewardEvent(vals[0].operator, "340", "0", "100", "100", "0"))
		},
		"delegator credits that differ from the validator's total": func(vals []fakeValidator, d string) []abci.Event {
			return append(rewardEvents(t), validatorRewardEvent(vals[0].operator, "340", "0", "290", "50", "0"), delegatorRewardEvent(vals[0].operator, d, "10"))
		},
		"a validator that is no operator address": func(vals []fakeValidator, d string) []abci.Event {
			return append(rewardEvents(t), validatorRewardEvent("nobody", "340", "0", "340", "0", "0"))
		},
		"delegator credits for a validator that was not reported as paid": func(vals []fakeValidator, d string) []abci.Event {
			return append(rewardEvents(t), validatorRewardEvent(vals[0].operator, "340", "0", "340", "0", "0"), delegatorRewardEvent(vals[1].operator, d, "5"))
		},
		"delegator credits and no validator reported as paid": func(vals []fakeValidator, d string) []abci.Event {
			return append(rewardEvents(t), delegatorRewardEvent(vals[0].operator, d, "5"))
		},
		"an amount that is not an integer": func(vals []fakeValidator, d string) []abci.Event {
			return append(rewardEvents(t), validatorRewardEvent(vals[0].operator, "many", "0", "0", "0", "0"))
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			chain, vals := econChain(t)
			for h := int64(1); h <= 3; h++ {
				var events []abci.Event
				if h == 3 {
					events = build(vals, addr(t, 7))
				}
				chain.addBlock(votes(chain.valset, true, true, true), events)
			}
			store := openStore(t, t.TempDir())
			defer store.Close()
			f, err := NewFollower(chain, store, 1)
			require.NoError(t, err)
			_, err = f.Step(context.Background())
			require.Error(t, err)
			st, serr := store.Status()
			require.NoError(t, serr)
			require.Equal(t, int64(2), st.Cursor, "the closing block is not indexed: no figure the chain does not give")
			require.NotNil(t, st.Stuck)
		})
	}
}

func TestRewardsAPI_servesEpochValidatorAndAccountRewards(t *testing.T) {
	chain, vals, delegator := rewardChain(t, true)
	store, _ := follow(t, chain, t.TempDir(), 1)
	defer store.Close()
	api := NewAPI(store, fixedTip{earliest: 1, latest: 7})

	code, body := get(t, api, http.MethodGet, "/index/v1/epochs/2/rewards?limit=5")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, body["recorded"])
	require.Len(t, body["rewards"], 2)

	code, body = get(t, api, http.MethodGet, "/index/v1/epochs/1/rewards")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, false, body["recorded"])
	require.Empty(t, body["rewards"], "an epoch the chain did not report is not recorded, whatever the page says")

	code, _ = get(t, api, http.MethodGet, "/index/v1/epochs/9/rewards")
	require.Equal(t, http.StatusNotFound, code, "an epoch still open")
	code, _ = get(t, api, http.MethodGet, "/index/v1/epochs/0/rewards")
	require.Equal(t, http.StatusBadRequest, code)
	code, _ = get(t, api, http.MethodGet, "/index/v1/epochs/1/rewards?limit=101")
	require.Equal(t, http.StatusBadRequest, code)

	code, body = get(t, api, http.MethodGet, "/index/v1/validators/"+vals[0].operator+"/rewards")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, body["rewards"], 2)
	code, _ = get(t, api, http.MethodGet, "/index/v1/validators/"+vals[0].operator[:len(vals[0].operator)-1]+"x/rewards")
	require.Equal(t, http.StatusBadRequest, code)

	code, body = get(t, api, http.MethodGet, "/index/v1/accounts/"+delegator+"/rewards")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 2.0, body["recorded_from_epoch"])
	require.Len(t, body["rewards"], 1)
	code, body = get(t, api, http.MethodGet, "/index/v1/accounts/"+addr(t, 8)+"/rewards")
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, body["rewards"], "an account that was credited nothing is an empty list, not an error")
	code, _ = get(t, api, http.MethodGet, "/index/v1/accounts/ORAMA1X/rewards")
	require.Equal(t, http.StatusBadRequest, code)
}
