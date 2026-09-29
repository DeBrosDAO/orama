package keeper_test

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/houses/keeper"
	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

func TestTiersStayClosedUntilOpeningRulesHold(t *testing.T) {
	exit := types.DefaultBootstrapExitStake()
	below := exit.SubRaw(1)

	t.Run("bootstrap", func(t *testing.T) {
		f := newTestFixture(t)
		view := mustTiers(t, f)
		require.False(t, view.Parameter)
		require.False(t, view.Structural)
		_, err := f.Keeper.SubmitProposal(f.Ctx, acc(1), parameterContent())
		require.ErrorIs(t, err, types.ErrTierClosed)
	})

	t.Run("parameter opens on stake or lambda only with a live house", func(t *testing.T) {
		cases := []struct {
			name      string
			bonded    math.Int
			lambda    string
			operators int
			wantOpen  bool
		}{
			{"stake below and lambda below", below, "0.5", 21, false},
			{"stake met but house empty", exit, "0", 0, false},
			{"stake met but house short", exit, "0", 20, false},
			{"lambda met but house short", math.ZeroInt(), "1", 20, false},
			{"stake met and house live", exit, "0", 21, true},
			{"lambda met and house live", math.ZeroInt(), "1", 21, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				f := newTestFixture(t)
				f.Staking.total = tc.bonded
				f.Power.lambda = math.LegacyMustNewDecFromStr(tc.lambda)
				if tc.operators > 0 {
					f.seatHouse(t, tc.operators)
				}
				view := mustTiers(t, f)
				require.Equal(t, tc.wantOpen, view.Parameter)
				_, err := f.Keeper.SubmitProposal(f.Ctx, acc(50), parameterContent())
				if tc.wantOpen {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, types.ErrTierClosed)
				}
			})
		}
	})

	t.Run("structural needs lambda and diversity", func(t *testing.T) {
		f := newTestFixture(t)
		f.Power.lambda = math.LegacyMustNewDecFromStr("0.999")
		f.Staking.total = exit
		f.seatHouse(t, 21)
		view := mustTiers(t, f)
		require.True(t, view.Parameter, "parameter tier does not require lambda 1")
		require.False(t, view.Structural)
		_, err := f.Keeper.SubmitProposal(f.Ctx, acc(50), spendContent(math.NewInt(1)))
		require.ErrorIs(t, err, types.ErrTierClosed)

		f.Power.lambda = math.LegacyOneDec()
		view = mustTiers(t, f)
		require.True(t, view.Structural)
		require.Equal(t, 7, view.Prefix16s)
		require.Equal(t, 5, view.ASNs)
		_, err = f.Keeper.SubmitProposal(f.Ctx, acc(50), spendContent(math.NewInt(1)))
		require.NoError(t, err)
	})
}

func TestSmallOperatorHouseBlocksStructuralVotes(t *testing.T) {
	f := newTestFixture(t)
	f.Power.lambda = math.LegacyOneDec()
	f.Staking.total = types.DefaultBootstrapExitStake()

	_, err := f.Keeper.SubmitProposal(f.Ctx, acc(50), spendContent(math.NewInt(1)))
	require.ErrorIs(t, err, types.ErrTierClosed)

	f.seatHouse(t, 20)
	view := mustTiers(t, f)
	require.Equal(t, 20, view.Eligible)
	require.False(t, view.Structural)
	_, err = f.Keeper.SubmitProposal(f.Ctx, acc(50), spendContent(math.NewInt(1)))
	require.ErrorIs(t, err, types.ErrTierClosed)

	// 21 identities that diversity does not cover, with the cap raised so the
	// cap itself is not what rejects them.
	f = newTestFixture(t)
	f.Power.lambda = math.LegacyOneDec()
	require.NoError(t, setCaps(f, 21, 21))
	ops := make([]types.OperatorInfo, 21)
	for i := range ops {
		ops[i] = types.OperatorInfo{
			Address: acc(i + 1), Prefix16: fmt.Sprintf("10.%d.0.0/16", i%6),
			ASN: uint32((i % 5) + 1), ServiceDays: types.MinServiceDays,
		}
	}
	f.Operators.ops = ops
	f.lockOperators(t, ops)
	view = mustTiers(t, f)
	require.Equal(t, 21, view.Eligible)
	require.Equal(t, 6, view.Prefix16s)
	require.False(t, view.Structural)
	_, err = f.Keeper.SubmitProposal(f.Ctx, acc(50), spendContent(math.NewInt(1)))
	require.ErrorIs(t, err, types.ErrTierClosed)

	f = newTestFixture(t)
	f.Power.lambda = math.LegacyOneDec()
	require.NoError(t, setCaps(f, 3, 21))
	ops = spreadOperators(21)
	for i := range ops {
		ops[i].ASN = uint32((i % 4) + 1)
	}
	f.Operators.ops = ops
	f.lockOperators(t, ops)
	view = mustTiers(t, f)
	require.Equal(t, 21, view.Eligible)
	require.Equal(t, 7, view.Prefix16s)
	require.Equal(t, 4, view.ASNs)
	require.False(t, view.Structural)
}

func TestOperatorHousePrefix16AndASNCaps(t *testing.T) {
	f := newTestFixture(t)
	f.Power.lambda = math.LegacyOneDec()
	ops := spreadOperators(21)
	f.Operators.ops = ops
	f.lockOperators(t, ops)
	view := mustTiers(t, f)
	require.Equal(t, 21, view.Eligible)

	// Later lock on a full /16 loses to the earlier locks.
	extraPrefix := types.OperatorInfo{
		Address: acc(100), Prefix16: ops[0].Prefix16, ASN: 9, ServiceDays: types.MinServiceDays,
	}
	// Later lock on a fresh /16 but a full ASN loses to the ASN cap.
	extraASN := types.OperatorInfo{
		Address: acc(101), Prefix16: "10.99.0.0/16", ASN: 1, ServiceDays: types.MinServiceDays,
	}
	young := types.OperatorInfo{
		Address: acc(102), Prefix16: "10.98.0.0/16", ASN: 9, ServiceDays: types.MinServiceDays - 1,
	}
	f.Ctx = f.Ctx.WithBlockHeight(2)
	f.Operators.ops = append(ops, extraPrefix, extraASN, young)
	bond := types.DefaultHouseBond()
	for _, op := range []types.OperatorInfo{extraPrefix, extraASN, young} {
		f.Bank.fund(op.Address.String(), bond)
		require.NoError(t, f.Keeper.LockHouseBond(f.Ctx, op.Address, bond))
	}
	view = mustTiers(t, f)
	require.Equal(t, 21, view.Eligible)
	require.Equal(t, 7, view.Prefix16s)

	f.selfBond(acc(200), math.NewInt(1_000))
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), parameterContent())
	require.NoError(t, err)
	_, err = f.Keeper.VoteOperator(f.Ctx, extraPrefix.Address, id, types.VoteOption_NO)
	require.ErrorIs(t, err, types.ErrNotEligible)
	_, err = f.Keeper.VoteOperator(f.Ctx, extraASN.Address, id, types.VoteOption_NO)
	require.ErrorIs(t, err, types.ErrNotEligible)
	_, err = f.Keeper.VoteOperator(f.Ctx, young.Address, id, types.VoteOption_NO)
	require.ErrorIs(t, err, types.ErrNotEligible)
	_, err = f.Keeper.VoteOperator(f.Ctx, ops[0].Address, id, types.VoteOption_NO)
	require.NoError(t, err)

	// An earlier lock takes the seat from a later one on the same /16.
	f = newTestFixture(t)
	f.Operators.ops = append([]types.OperatorInfo{extraPrefix}, spreadOperators(21)...)
	f.Bank.fund(extraPrefix.Address.String(), bond)
	require.NoError(t, f.Keeper.LockHouseBond(f.Ctx, extraPrefix.Address, bond))
	f.Ctx = f.Ctx.WithBlockHeight(5)
	f.lockOperators(t, spreadOperators(21))
	view = mustTiers(t, f)
	require.Equal(t, 21, view.Eligible)
	f.Power.lambda = math.LegacyOneDec()
	f.selfBond(acc(200), math.NewInt(1_000))
	id, err = f.Keeper.SubmitProposal(f.Ctx, acc(200), parameterContent())
	require.NoError(t, err)
	_, err = f.Keeper.VoteOperator(f.Ctx, extraPrefix.Address, id, types.VoteOption_YES)
	require.NoError(t, err)
	dropped := lastAddress(acc(1), acc(2), acc(3))
	_, err = f.Keeper.VoteOperator(f.Ctx, dropped, id, types.VoteOption_YES)
	require.ErrorIs(t, err, types.ErrNotEligible, "the latest address on the full /16 is the one the cap drops")
}

func TestHouseBondLockAndSlashOnEquivocation(t *testing.T) {
	f := newTestFixture(t)
	f.Power.lambda = math.LegacyOneDec()
	f.seatHouse(t, 21)
	f.selfBond(acc(200), math.NewInt(1_000))
	bond := types.DefaultHouseBond()
	operator := acc(1)

	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), parameterContent())
	require.NoError(t, err)
	_, err = f.Keeper.VoteOperator(f.Ctx, operator, id, types.VoteOption_YES)
	require.NoError(t, err)
	require.ErrorIs(t, f.Keeper.UnlockHouseBond(f.Ctx, operator), types.ErrBondLocked)

	slashed, err := f.Keeper.VoteOperator(f.Ctx, operator, id, types.VoteOption_NO)
	require.NoError(t, err)
	require.True(t, slashed)
	require.True(t, f.Bank.burned.Equal(bond))
	_, err = f.Keeper.Bonds.Get(f.Ctx, operator.String())
	require.Error(t, err)
	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, got.BondsMatchModule, got.Detail)

	f.Bank.fund(operator.String(), bond)
	require.NoError(t, f.Keeper.LockHouseBond(f.Ctx, operator, bond))
	_, err = f.Keeper.VoteOperator(f.Ctx, operator, id, types.VoteOption_YES)
	require.ErrorIs(t, err, types.ErrNotEligible)

	require.NoError(t, f.Keeper.VoteToken(f.Ctx, acc(200), id, types.VoteOption_YES))
	f.advance(t, 24*time.Hour)
	f.advance(t, 7*24*time.Hour)
	p := f.proposal(t, id)
	require.Equal(t, types.ProposalStatus_TIMELOCK, p.Status)
	require.Zero(t, p.OperatorYes, "an equivocating operator's vote does not count after they relock")
}

func TestVetoWindow(t *testing.T) {
	t.Run("six of twenty-one is short of 30 percent", func(t *testing.T) {
		f := openParameter(t)
		id := f.submitParameter(t)
		f.advance(t, 24*time.Hour)
		require.Equal(t, types.ProposalStatus_VETO_WINDOW, f.proposal(t, id).Status)
		for i := 1; i <= 6; i++ {
			_, err := f.Keeper.VoteOperator(f.Ctx, acc(i), id, types.VoteOption_NO)
			require.NoError(t, err)
		}
		f.advance(t, 7*24*time.Hour)
		p := f.proposal(t, id)
		require.Equal(t, types.ProposalStatus_TIMELOCK, p.Status)
		require.Equal(t, uint64(6), p.OperatorNo)
	})

	t.Run("seven of twenty-one vetoes", func(t *testing.T) {
		f := openParameter(t)
		id := f.submitParameter(t)
		f.advance(t, 24*time.Hour)
		for i := 1; i <= 7; i++ {
			_, err := f.Keeper.VoteOperator(f.Ctx, acc(i), id, types.VoteOption_NO)
			require.NoError(t, err)
		}
		f.advance(t, 7*24*time.Hour)
		p := f.proposal(t, id)
		require.Equal(t, types.ProposalStatus_REJECTED, p.Status)
		require.Equal(t, "operator house veto", p.FailReason)
	})

	t.Run("a vote after seven days does not count", func(t *testing.T) {
		f := openParameter(t)
		id := f.submitParameter(t)
		f.advance(t, 24*time.Hour)
		for i := 1; i <= 6; i++ {
			_, err := f.Keeper.VoteOperator(f.Ctx, acc(i), id, types.VoteOption_NO)
			require.NoError(t, err)
		}
		f.advance(t, 7*24*time.Hour)
		_, err := f.Keeper.VoteOperator(f.Ctx, acc(7), id, types.VoteOption_NO)
		require.Error(t, err)
		require.Equal(t, types.ProposalStatus_TIMELOCK, f.proposal(t, id).Status)
	})
}

func TestDelegatedVoteCap(t *testing.T) {
	f := openParameter(t)
	validator := acc(80)
	delegator := acc(81)
	filler := acc(82)
	bonded := math.NewInt(10_000)
	f.Staking.total = bonded
	f.Staking.dels = []types.BondedDelegation{
		{Delegator: delegator, Validator: validator, Amount: math.NewInt(4_000)},
		{Delegator: filler, Validator: filler, Amount: math.NewInt(6_000)},
	}
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), parameterContent())
	require.NoError(t, err)
	require.NoError(t, f.Keeper.VoteToken(f.Ctx, validator, id, types.VoteOption_YES))
	f.advance(t, 24*time.Hour)
	p := f.proposal(t, id)
	require.True(t, p.TokenYes.Equal(math.NewInt(300)), "4000 delegated must tally as 3%% of 10000, got %s", p.TokenYes)
	require.True(t, p.TokenNo.IsZero())
	require.Equal(t, types.ProposalStatus_REJECTED, p.Status, "capped weight must not meet quorum by itself")
}

func TestDirectVoteOverridesValidator(t *testing.T) {
	t.Run("direct vote leaves the validator bucket", func(t *testing.T) {
		f := openParameter(t)
		validator := acc(80)
		delegator := acc(81)
		filler := acc(82)
		f.Staking.total = math.NewInt(10_000)
		f.Staking.dels = []types.BondedDelegation{
			{Delegator: delegator, Validator: validator, Amount: math.NewInt(200)},
			{Delegator: filler, Validator: filler, Amount: math.NewInt(9_800)},
		}
		id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), parameterContent())
		require.NoError(t, err)
		require.NoError(t, f.Keeper.VoteToken(f.Ctx, validator, id, types.VoteOption_YES))
		require.NoError(t, f.Keeper.VoteToken(f.Ctx, delegator, id, types.VoteOption_NO))
		f.advance(t, 24*time.Hour)
		p := f.proposal(t, id)
		require.True(t, p.TokenYes.IsZero(), "inherited yes without the direct voter, got %s", p.TokenYes)
		require.True(t, p.TokenNo.Equal(math.NewInt(200)), "got %s", p.TokenNo)
	})

	t.Run("direct vote is not capped at 3 percent", func(t *testing.T) {
		f := openParameter(t)
		validator := acc(80)
		delegator := acc(81)
		filler := acc(82)
		f.Staking.total = math.NewInt(10_000)
		f.Staking.dels = []types.BondedDelegation{
			{Delegator: delegator, Validator: validator, Amount: math.NewInt(4_000)},
			{Delegator: filler, Validator: filler, Amount: math.NewInt(6_000)},
		}
		id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), parameterContent())
		require.NoError(t, err)
		require.NoError(t, f.Keeper.VoteToken(f.Ctx, validator, id, types.VoteOption_YES))
		require.NoError(t, f.Keeper.VoteToken(f.Ctx, delegator, id, types.VoteOption_NO))
		f.advance(t, 24*time.Hour)
		p := f.proposal(t, id)
		require.True(t, p.TokenYes.IsZero())
		require.True(t, p.TokenNo.Equal(math.NewInt(4_000)), "direct stake is uncapped, got %s", p.TokenNo)
	})
}

func TestTimelocksAndNoEarlyExecution(t *testing.T) {
	f := openStructural(t)
	whale := acc(200)

	paramID := f.submitParameter(t)
	f.advance(t, 24*time.Hour)
	f.advance(t, 7*24*time.Hour)
	require.Equal(t, types.ProposalStatus_TIMELOCK, f.proposal(t, paramID).Status)
	require.Error(t, f.Keeper.ExecuteProposal(f.Ctx, paramID))
	f.advance(t, 14*24*time.Hour-time.Second)
	require.Equal(t, types.ProposalStatus_TIMELOCK, f.proposal(t, paramID).Status)
	before, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	f.advance(t, time.Second)
	p := f.proposal(t, paramID)
	require.Equal(t, types.ProposalStatus_EXECUTED, p.Status)
	after, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, after.BootstrapExitStake.Equal(before.BootstrapExitStake))

	upgradeID, err := f.Keeper.SubmitProposal(f.Ctx, whale, types.ProposalContent{SoftwareUpgrade: &types.SoftwareUpgrade{Name: "v1", Height: 1_000}})
	require.NoError(t, err)
	f.passStructuralVotes(t, upgradeID)
	f.advance(t, 24*time.Hour)
	require.Equal(t, types.ProposalStatus_TIMELOCK, f.proposal(t, upgradeID).Status)
	f.advance(t, 60*24*time.Hour-time.Second)
	require.Nil(t, mustEnacted(t, f).ScheduledUpgrade)
	f.advance(t, time.Second)
	require.Equal(t, types.ProposalStatus_EXECUTED, f.proposal(t, upgradeID).Status)
	require.Equal(t, "v1", mustEnacted(t, f).ScheduledUpgrade.Name)

	f.Emission.ceiling[1] = math.NewInt(100)
	spendID, err := f.Keeper.SubmitProposal(f.Ctx, whale, spendContent(math.NewInt(40)))
	require.NoError(t, err)
	f.passStructuralVotes(t, spendID)
	f.advance(t, 24*time.Hour)
	require.Equal(t, types.ProposalStatus_TIMELOCK, f.proposal(t, spendID).Status)
	require.Zero(t, f.Emission.calls)
	f.advance(t, 7*24*time.Hour-time.Second)
	require.Zero(t, f.Emission.calls)
	f.advance(t, time.Second)
	require.Equal(t, types.ProposalStatus_EXECUTED, f.proposal(t, spendID).Status)
	require.Equal(t, 1, f.Emission.calls)
	require.True(t, f.Emission.minted[1].Equal(math.NewInt(40)))
	require.Len(t, f.Earnings.credits, 1)
	require.Equal(t, "emission", f.Earnings.credits[0].module)
	require.True(t, f.Earnings.credits[0].amount.Equal(math.NewInt(40)))
}

func TestSpendMintsOnlyWhenApprovedAndWithinCeiling(t *testing.T) {
	f := openStructural(t)
	f.Emission.ceiling[1] = math.NewInt(100)

	rejected, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), spendContent(math.NewInt(40)))
	require.NoError(t, err)
	require.Error(t, f.Keeper.ExecuteProposal(f.Ctx, rejected))
	f.advance(t, 24*time.Hour)
	require.Equal(t, types.ProposalStatus_REJECTED, f.proposal(t, rejected).Status)
	require.Zero(t, f.Emission.calls)
	require.Empty(t, f.Earnings.credits)

	over, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), spendContent(math.NewInt(101)))
	require.NoError(t, err)
	f.passStructuralVotes(t, over)
	f.advance(t, 24*time.Hour)
	f.advance(t, 7*24*time.Hour)
	require.Equal(t, types.ProposalStatus_FAILED, f.proposal(t, over).Status)
	require.True(t, f.Emission.minted[1].IsNil() || f.Emission.minted[1].IsZero())
	require.Empty(t, f.Earnings.credits)

	okID, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), spendContent(math.NewInt(100)))
	require.NoError(t, err)
	f.passStructuralVotes(t, okID)
	f.advance(t, 24*time.Hour)
	f.advance(t, 7*24*time.Hour)
	require.Equal(t, types.ProposalStatus_EXECUTED, f.proposal(t, okID).Status)
	require.True(t, f.Emission.minted[1].Equal(math.NewInt(100)))
	require.Len(t, f.Earnings.credits, 1)

	again, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), spendContent(math.NewInt(1)))
	require.NoError(t, err)
	f.passStructuralVotes(t, again)
	f.advance(t, 24*time.Hour)
	f.advance(t, 7*24*time.Hour)
	require.Equal(t, types.ProposalStatus_FAILED, f.proposal(t, again).Status)
	require.True(t, f.Emission.minted[1].Equal(math.NewInt(100)))
	require.Len(t, f.Earnings.credits, 1)
}

func TestSplitStaysInsideCodedBoundsAndMIsOneWay(t *testing.T) {
	f := openStructural(t)
	_, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{EmissionSplit: &types.EmissionSplitChange{
		ValidatorPercent: 100, StoragePercent: 0, RelayPercent: 0, DevelopmentPercent: 0,
	}})
	require.Error(t, err)

	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{EmissionSplit: &types.EmissionSplitChange{
		ValidatorPercent: 70, StoragePercent: 15, RelayPercent: 10, DevelopmentPercent: 5,
	}})
	require.NoError(t, err)
	f.passStructuralVotes(t, id)
	f.advance(t, 24*time.Hour)
	f.advance(t, 60*24*time.Hour)
	require.Equal(t, uint32(70), mustEnacted(t, f).EmissionSplit.ValidatorPercent)

	activate, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{PowerBounds: &types.PowerBoundsChange{
		MMax: math.LegacyMustNewDecFromStr("1.10"), ActivateM: true,
	}})
	require.NoError(t, err)
	f.passStructuralVotes(t, activate)
	f.advance(t, 24*time.Hour)
	f.advance(t, 60*24*time.Hour)
	enacted := mustEnacted(t, f)
	require.True(t, enacted.MActivated)
	require.True(t, enacted.MMax.Equal(math.LegacyMustNewDecFromStr("1.10")))

	lower, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{PowerBounds: &types.PowerBoundsChange{
		MMax: math.LegacyMustNewDecFromStr("0.90"), ActivateM: false,
	}})
	require.NoError(t, err)
	f.passStructuralVotes(t, lower)
	f.advance(t, 24*time.Hour)
	f.advance(t, 60*24*time.Hour)
	enacted = mustEnacted(t, f)
	require.True(t, enacted.MActivated, "activate_m false must not turn M off")
	require.True(t, enacted.MMax.Equal(math.LegacyMustNewDecFromStr("0.90")))
}

func TestMsgServerSubmitAndInvariants(t *testing.T) {
	f := openParameter(t)
	srv := keeper.NewMsgServer(f.Keeper)
	res, err := srv.SubmitProposal(f.Ctx, &types.MsgSubmitProposal{
		Proposer: acc(200).String(),
		Content:  parameterContent(),
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.ProposalId)
	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, got.BondsMatchModule, got.Detail)

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.Bonds, 21)
}

func openParameter(t *testing.T) *testFixture {
	t.Helper()
	f := newTestFixture(t)
	// Lambda keeps the parameter tier open when a test replaces bonded stake.
	f.Power.lambda = math.LegacyOneDec()
	f.seatHouse(t, 21)
	f.selfBond(acc(200), math.NewInt(1_000))
	return f
}

func openStructural(t *testing.T) *testFixture {
	t.Helper()
	f := openParameter(t)
	f.Power.lambda = math.LegacyOneDec()
	return f
}

func (f *testFixture) submitParameter(t *testing.T) uint64 {
	t.Helper()
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), parameterContent())
	require.NoError(t, err)
	require.NoError(t, f.Keeper.VoteToken(f.Ctx, acc(200), id, types.VoteOption_YES))
	return id
}

func (f *testFixture) passStructuralVotes(t *testing.T, id uint64) {
	t.Helper()
	require.NoError(t, f.Keeper.VoteToken(f.Ctx, acc(200), id, types.VoteOption_YES))
	for i := 1; i <= 11; i++ {
		_, err := f.Keeper.VoteOperator(f.Ctx, acc(i), id, types.VoteOption_YES)
		require.NoError(t, err)
	}
}

func spendContent(amount math.Int) types.ProposalContent {
	return types.ProposalContent{DevelopmentSpend: &types.DevelopmentSpend{
		Recipient: acc(200).String(),
		Amount:    amount,
		Epoch:     1,
	}}
}

func mustTiers(t *testing.T, f *testFixture) keeper.TierView {
	t.Helper()
	view, err := f.Keeper.Tiers(f.Ctx)
	require.NoError(t, err)
	return view
}

func mustEnacted(t *testing.T, f *testFixture) types.Enacted {
	t.Helper()
	enacted, err := f.Keeper.Enacted.Get(f.Ctx)
	require.NoError(t, err)
	return enacted
}

func setCaps(f *testFixture, prefix, asn uint32) error {
	p, err := f.Keeper.Params.Get(f.Ctx)
	if err != nil {
		return err
	}
	p.MaxEligiblePerPrefix16 = prefix
	p.MaxEligiblePerAsn = asn
	if err := p.Validate(); err != nil {
		return err
	}
	return f.Keeper.Params.Set(f.Ctx, p)
}

func lastAddress(addrs ...sdk.AccAddress) sdk.AccAddress {
	sort.Slice(addrs, func(i, j int) bool { return addrs[i].String() < addrs[j].String() })
	return addrs[len(addrs)-1]
}

// A proposal whose tally cannot be read is closed as FAILED with the reason; it must not fail
// EndBlock, which would halt every validator over one proposal, and it must not hold up the others.
func TestAdvance_oneUnreadableProposalDoesNotHaltTheBlock(t *testing.T) {
	f := openParameter(t)
	id := f.submitParameter(t)
	f.Staking.failTotal = true
	f.advance(t, 24*time.Hour)
	p := f.proposal(t, id)
	require.Equal(t, types.ProposalStatus_FAILED, p.Status)
	require.Contains(t, p.FailReason, "staking store unreadable")
	var reported bool
	for _, e := range f.Ctx.EventManager().Events() {
		reported = reported || e.Type == "houses_proposal_failed"
	}
	require.True(t, reported)
	f.Staking.failTotal = false
	f.advance(t, time.Hour)
}
