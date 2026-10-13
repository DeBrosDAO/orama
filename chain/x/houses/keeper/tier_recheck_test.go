package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// The structural tier (>= min_house_size eligible operators, 7 /16s, 5 ASNs) is checked only
// when a proposal is submitted. At the close of voting the operator majority is computed over
// whatever house is left, so non-voting operators who unlock their bond shrink the denominator and
// 8 YES votes pass a proposal the house of 21 never approved.
func TestStructuralProposalIsRejectedWhenTheHouseFellBelowTheOpeningRule(t *testing.T) {
	f := openStructural(t)
	f.Emission.ceiling[1] = math.NewInt(100)
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), spendContent(math.NewInt(40)))
	require.NoError(t, err)
	require.NoError(t, f.Keeper.VoteToken(f.Ctx, acc(200), id, types.VoteOption_YES))
	for i := 1; i <= 8; i++ {
		_, err := f.Keeper.VoteOperator(f.Ctx, acc(i), id, types.VoteOption_YES)
		require.NoError(t, err)
	}
	// 13 of the 21 operators did not vote; they take their bonds back.
	for i := 9; i <= 21; i++ {
		require.NoError(t, f.Keeper.UnlockHouseBond(f.Ctx, acc(i)))
	}
	view := mustTiers(t, f)
	require.Equal(t, 8, view.Eligible)
	require.False(t, view.Structural, "the structural tier is closed now")

	f.advance(t, 24*time.Hour)
	require.Equal(t, types.ProposalStatus_REJECTED, f.proposal(t, id).Status,
		"a structural proposal must not pass while the operator house is under its opening rule")
}

// The same rule holds after the timelock: operators who unlock their bonds while an approved
// structural proposal waits take the house under the opening rule, and the proposal is not run.
func TestStructuralProposalIsNotExecutedWhenTheHouseFellBelowTheOpeningRuleInItsTimelock(t *testing.T) {
	f := openStructural(t)
	f.Emission.ceiling[1] = math.NewInt(100)
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), spendContent(math.NewInt(40)))
	require.NoError(t, err)
	require.NoError(t, f.Keeper.VoteToken(f.Ctx, acc(200), id, types.VoteOption_YES))
	for i := 1; i <= 21; i++ {
		_, err := f.Keeper.VoteOperator(f.Ctx, acc(i), id, types.VoteOption_YES)
		require.NoError(t, err)
	}
	f.advance(t, 8*24*time.Hour)
	require.Equal(t, types.ProposalStatus_TIMELOCK, f.proposal(t, id).Status, "the full house approved it")

	for i := 9; i <= 21; i++ {
		require.NoError(t, f.Keeper.UnlockHouseBond(f.Ctx, acc(i)))
	}
	require.False(t, mustTiers(t, f).Structural)

	f.advance(t, 30*24*time.Hour)
	got := f.proposal(t, id)
	require.Equal(t, types.ProposalStatus_FAILED, got.Status)
	require.Contains(t, got.FailReason, types.ErrTierClosed.Error())
}

// Eligibility is read from the house bonds: an operator that holds no bond is never looked up, so the
// cost of reading the house does not grow with the operators that registered and bonded nothing.
func TestEligibleSetLooksUpOnlyTheOperatorsThatHoldABond(t *testing.T) {
	f := openStructural(t)
	registered := f.Operators.ops
	for i := 1000; i < 1500; i++ {
		registered = append(registered, types.OperatorInfo{
			Address: acc(i), Prefix16: "10.99.0.0/16", ASN: 99, ServiceDays: types.MinServiceDays,
		})
	}
	f.Operators.ops = registered
	f.Operators.lookups = 0

	view := mustTiers(t, f)
	require.Equal(t, 21, view.Eligible)
	require.Equal(t, 21, f.Operators.lookups, "one lookup per house bond, none for the 500 operators with no bond")
}
