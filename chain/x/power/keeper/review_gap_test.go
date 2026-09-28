package keeper_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	protov2 "google.golang.org/protobuf/proto"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	powerante "github.com/DeBrosOfficial/network/chain/x/power/ante"
	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

func TestRunEndBlock_jailedCommitteeMemberLosesPower(t *testing.T) {
	f := newTestFixture(t)
	first, second := setupTwoCommitteeMembers(t, f)

	f.Emission.epoch = 1
	_, err := f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)

	f.Staking.jail(t, sdk.ValAddress(first).String())
	updates, err := f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)

	_, err = f.Keeper.LastPower.Get(f.Ctx, sdk.ValAddress(first).String())
	require.ErrorContains(t, err, "not found", "a jailed committee member must be removed from the validator set")

	power, err := f.Keeper.LastPower.Get(f.Ctx, sdk.ValAddress(second).String())
	require.NoError(t, err)
	require.Positive(t, power, "the other committee member must keep the chain's voting power")
	require.NotEmpty(t, updates)
}

func TestRunEndBlock_tombstonedCommitteeMemberLosesPower(t *testing.T) {
	f := newTestFixture(t)
	first, second := setupTwoCommitteeMembers(t, f)

	f.Emission.epoch = 1
	_, err := f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)

	f.Slashing.tombstone(testPubKey(1))
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)

	_, err = f.Keeper.LastPower.Get(f.Ctx, sdk.ValAddress(first).String())
	require.ErrorContains(t, err, "not found", "a tombstoned committee member must lose its seat")
	power, err := f.Keeper.LastPower.Get(f.Ctx, sdk.ValAddress(second).String())
	require.NoError(t, err)
	require.Positive(t, power)
}

func TestRunEndBlock_zeroCappedShareResetsRampWhileStillBonded(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)

	whale := sdk.ValAddress("ramp_reset_whale_val").String()
	dust := sdk.ValAddress("ramp_reset_dust_valx").String()
	f.Staking.addValidator(t, whale, testPubKey(2), 1_000_000, "0.0")
	f.Staking.addValidator(t, dust, testPubKey(3), 1, "0.0")

	f.Emission.epoch = 3
	_, err := f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	_, err = f.Keeper.RampActivation.Get(f.Ctx, dust)
	require.NoError(t, err, "a positive share stamps a ramp clock")

	// Keep the dust validator bonded, but with zero tokens, while the whale still
	// has stake. Its capped share is then zero and the ramp clock must be dropped.
	// The default 5% cap cannot be met by two validators (cap*n < 1), so the
	// equal fallback would keep a zero-token validator's share positive.
	// A cap both can satisfy makes a zero-token share actually zero.
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.CapFractionNormal = math.LegacyNewDecWithPrec(60, 2)
	p.CapFractionReduced = math.LegacyNewDecWithPrec(60, 2)
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	fv := f.Staking.validators[dust]
	fv.val.Tokens = math.ZeroInt()
	fv.val.DelegatorShares = math.LegacyZeroDec()
	fv.val.Status = stakingtypes.Bonded
	fv.powerIndexed = true

	f.Emission.epoch = 4
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	_, err = f.Keeper.RampActivation.Get(f.Ctx, dust)
	require.ErrorContains(t, err, "not found")
	_, err = f.Keeper.RampAdmitted.Get(f.Ctx, dust)
	require.ErrorContains(t, err, "not found", "a zero share must drop admitted tokens, not fast-track the next bond")

	fv.val.Tokens = math.NewInt(1_000_000)
	fv.val.DelegatorShares = math.LegacyNewDec(1_000_000)
	f.Emission.epoch = 5
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	activation, err := f.Keeper.RampActivation.Get(f.Ctx, dust)
	require.NoError(t, err)
	require.Equal(t, uint64(5), activation, "returning to a positive share starts the ramp over")
}

func TestUndelegateGuard_locksForceBondedSelfStake(t *testing.T) {
	f := newTestFixture(t)
	member := setupSingleCommitteeGenesis(t, f)
	valoper := sdk.ValAddress(member).String()
	f.Staking.addValidator(t, valoper, testPubKey(1), 1_000, "0.0")
	require.NoError(t, f.Keeper.CommitteeSelfBond.Set(f.Ctx, valoper, math.NewInt(800)))

	guard := powerante.NewUndelegateGuard(f.Staking, f.Keeper)
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

	_, err := guard.AnteHandle(f.Ctx, guardTx(&stakingtypes.MsgUndelegate{
		DelegatorAddress: member.String(),
		ValidatorAddress: valoper,
		Amount:           sdk.NewCoin(params.BaseDenom, math.NewInt(300)),
	}), false, next)
	require.Error(t, err, "1000 - 300 = 700 is below the 800 locked force-bond")

	_, err = guard.AnteHandle(f.Ctx, guardTx(&stakingtypes.MsgUndelegate{
		DelegatorAddress: member.String(),
		ValidatorAddress: valoper,
		Amount:           sdk.NewCoin(params.BaseDenom, math.NewInt(200)),
	}), false, next)
	require.NoError(t, err, "1000 - 200 = 800 stays at the locked floor")

	outsider := sdk.AccAddress("ordinary_delegator___")
	_, err = guard.AnteHandle(f.Ctx, guardTx(&stakingtypes.MsgUndelegate{
		DelegatorAddress: outsider.String(),
		ValidatorAddress: valoper,
		Amount:           sdk.NewCoin(params.BaseDenom, math.NewInt(300)),
	}), false, next)
	require.NoError(t, err, "someone else's undelegation is not the force-bonded self-stake")

	require.NoError(t, f.Keeper.Lambda.Set(f.Ctx, math.LegacyOneDec()))
	_, err = guard.AnteHandle(f.Ctx, guardTx(&stakingtypes.MsgUndelegate{
		DelegatorAddress: member.String(),
		ValidatorAddress: valoper,
		Amount:           sdk.NewCoin(params.BaseDenom, math.NewInt(300)),
	}), false, next)
	require.NoError(t, err, "the lock ends once lambda reaches 1")
}

func TestMinDelegationDecorator_rejectsDustAndAllowsFullExit(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.MinDelegationForRewards = math.NewInt(100)
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	valoper := sdk.ValAddress("min_delegation_val__").String()
	delegator := sdk.AccAddress("min_delegation_del__")
	f.Staking.addValidator(t, valoper, testPubKey(2), 1_000, "0.0")

	dec := powerante.NewMinDelegationDecorator(f.Staking, f.Keeper)
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

	_, err = dec.AnteHandle(f.Ctx, guardTx(&stakingtypes.MsgDelegate{
		DelegatorAddress: delegator.String(),
		ValidatorAddress: valoper,
		Amount:           sdk.NewCoin(params.BaseDenom, math.NewInt(50)),
	}), false, next)
	require.Error(t, err)

	_, err = dec.AnteHandle(f.Ctx, guardTx(&stakingtypes.MsgDelegate{
		DelegatorAddress: delegator.String(),
		ValidatorAddress: valoper,
		Amount:           sdk.NewCoin(params.BaseDenom, math.NewInt(100)),
	}), false, next)
	require.NoError(t, err)

	operator, err := sdk.ValAddressFromBech32(valoper)
	require.NoError(t, err)
	_, err = dec.AnteHandle(f.Ctx, guardTx(&stakingtypes.MsgUndelegate{
		DelegatorAddress: sdk.AccAddress(operator).String(),
		ValidatorAddress: valoper,
		Amount:           sdk.NewCoin(params.BaseDenom, math.NewInt(1_000)),
	}), false, next)
	require.NoError(t, err, "a full exit to zero is allowed")
}

func TestMinDelegationDecorator_accumulatesMessagesAndResidualShares(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.MinDelegationForRewards = math.NewInt(100)
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	valoper := sdk.ValAddress("min_split_validator__").String()
	f.Staking.addValidator(t, valoper, testPubKey(4), 101, "0.0")
	operator := sdk.AccAddress(sdk.MustValAddressFromBech32(valoper))
	dec := powerante.NewMinDelegationDecorator(f.Staking, f.Keeper)
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

	_, err = dec.AnteHandle(f.Ctx, twoMsgTx{msgs: []sdk.Msg{
		stakingtypes.NewMsgUndelegate(operator.String(), valoper, sdk.NewCoin(params.BaseDenom, math.NewInt(1))),
		stakingtypes.NewMsgUndelegate(operator.String(), valoper, sdk.NewCoin(params.BaseDenom, math.NewInt(1))),
	}}, false, next)
	require.Error(t, err, "two 1-norama withdrawals from 101 must not both pass against the original balance")

	_, err = dec.AnteHandle(f.Ctx, guardTx(&stakingtypes.MsgCancelUnbondingDelegation{
		DelegatorAddress: operator.String(),
		ValidatorAddress: valoper,
		Amount:           sdk.NewCoin(params.BaseDenom, math.NewInt(1)),
	}), false, next)
	require.NoError(t, err, "cancelling 1 norama back onto a 101 delegation stays above the minimum")

	// 100 shares against a 300-share validator with 100 tokens are worth 33 tokens.
	// Withdrawing the truncated 33 leaves 1 share, whose truncated token value is 0.
	dustVal := sdk.ValAddress("min_residual_val____").String()
	f.Staking.addValidator(t, dustVal, testPubKey(5), 100, "0.0")
	dustOp := sdk.AccAddress(sdk.MustValAddressFromBech32(dustVal))
	fv := f.Staking.validators[dustVal]
	fv.val.Tokens = math.NewInt(100)
	fv.val.DelegatorShares = math.LegacyNewDec(300)
	fv.delegations[0].shares = math.LegacyNewDec(100)
	_, err = dec.AnteHandle(f.Ctx, guardTx(stakingtypes.NewMsgUndelegate(dustOp.String(), dustVal, sdk.NewCoin(params.BaseDenom, math.NewInt(33)))), false, next)
	require.Error(t, err, "a withdrawal that leaves a zero-token share remainder is not a full exit")
}

func TestUndelegateGuard_sumsWithdrawalsInOneTransaction(t *testing.T) {
	f := newTestFixture(t)
	member := setupSingleCommitteeGenesis(t, f)
	valoper := sdk.ValAddress(member).String()
	f.Staking.addValidator(t, valoper, testPubKey(1), 1_000, "0.0")
	require.NoError(t, f.Keeper.CommitteeSelfBond.Set(f.Ctx, valoper, math.NewInt(800)))
	guard := powerante.NewUndelegateGuard(f.Staking, f.Keeper)
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

	_, err := guard.AnteHandle(f.Ctx, twoMsgTx{msgs: []sdk.Msg{
		stakingtypes.NewMsgUndelegate(member.String(), valoper, sdk.NewCoin(params.BaseDenom, math.NewInt(150))),
		stakingtypes.NewMsgUndelegate(member.String(), valoper, sdk.NewCoin(params.BaseDenom, math.NewInt(150))),
	}}, false, next)
	require.Error(t, err, "150 + 150 withdraws below the 800 lock even though each 150 alone would not")
}

type twoMsgTx struct{ msgs []sdk.Msg }

func (t twoMsgTx) GetMsgs() []sdk.Msg                    { return t.msgs }
func (t twoMsgTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }

func guardTx(msg sdk.Msg) sdk.Tx { return guardMsgTx{msg: msg} }

type guardMsgTx struct{ msg sdk.Msg }

func (t guardMsgTx) GetMsgs() []sdk.Msg                    { return []sdk.Msg{t.msg} }
func (t guardMsgTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }

func TestMinDelegationDecorator_uppercaseAddressIsTheSameDelegation(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.MinDelegationForRewards = math.NewInt(100)
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	valoper := sdk.ValAddress("min_case_validator__").String()
	f.Staking.addValidator(t, valoper, testPubKey(6), 100, "0.0")
	operator := sdk.AccAddress(sdk.MustValAddressFromBech32(valoper))
	dec := powerante.NewMinDelegationDecorator(f.Staking, f.Keeper)
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

	require.NotPanics(t, func() {
		_, err = dec.AnteHandle(f.Ctx, twoMsgTx{msgs: []sdk.Msg{
			stakingtypes.NewMsgUndelegate(operator.String(), valoper, sdk.NewCoin(params.BaseDenom, math.NewInt(50))),
			stakingtypes.NewMsgUndelegate(strings.ToUpper(operator.String()), strings.ToUpper(valoper), sdk.NewCoin(params.BaseDenom, math.NewInt(60))),
		}}, false, next)
	})
	require.Error(t, err, "an uppercase repeat of the same undelegation must see the tokens already removed")
}

func TestMinDelegationDecorator_redelegateCreditsTokensActuallyRemoved(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.MinDelegationForRewards = math.NewInt(2)
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	src := sdk.ValAddress("redeleg_source_val__").String()
	dst := sdk.ValAddress("redeleg_dest_val____").String()
	f.Staking.addValidator(t, src, testPubKey(7), 3, "0.0")
	f.Staking.addValidator(t, dst, testPubKey(8), 0, "0.0")
	source := f.Staking.validators[src]
	source.val.Tokens = math.NewInt(3)
	source.val.DelegatorShares = math.LegacyNewDec(2)
	source.delegations[0].shares = math.LegacyNewDec(1)
	delegator := sdk.AccAddress(sdk.MustValAddressFromBech32(src))

	dec := powerante.NewMinDelegationDecorator(f.Staking, f.Keeper)
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }
	_, err = dec.AnteHandle(f.Ctx, guardTx(stakingtypes.NewMsgBeginRedelegate(
		delegator.String(), src, dst, sdk.NewCoin(params.BaseDenom, math.NewInt(2)),
	)), false, next)
	require.Error(t, err, "the destination must be credited the 1 token actually unbonded, which is below the minimum")
}

func TestUndelegateGuard_uppercaseAddressCountsTowardTheLock(t *testing.T) {
	f := newTestFixture(t)
	member := setupSingleCommitteeGenesis(t, f)
	valoper := sdk.ValAddress(member).String()
	f.Staking.addValidator(t, valoper, testPubKey(1), 1_000, "0.0")
	require.NoError(t, f.Keeper.CommitteeSelfBond.Set(f.Ctx, valoper, math.NewInt(800)))
	guard := powerante.NewUndelegateGuard(f.Staking, f.Keeper)
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

	_, err := guard.AnteHandle(f.Ctx, twoMsgTx{msgs: []sdk.Msg{
		stakingtypes.NewMsgUndelegate(member.String(), valoper, sdk.NewCoin(params.BaseDenom, math.NewInt(150))),
		stakingtypes.NewMsgUndelegate(strings.ToUpper(member.String()), strings.ToUpper(valoper), sdk.NewCoin(params.BaseDenom, math.NewInt(150))),
	}}, false, next)
	require.Error(t, err, "uppercase and lowercase forms of one withdrawal must be added together")
}

func TestRunEndBlock_stakeSpikeCannotJumpMoreThanOneThird(t *testing.T) {
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.RampEpochs = 30
	p.BootstrapExitStake = math.NewInt(1_000_000_000_000)
	p.BootstrapDeadlineEpochs = 1_000_000
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))

	anchor := sdk.ValAddress("spike_anchor_val____").String()
	f.Staking.addValidator(t, anchor, testPubKey(4), 1_000_000, "0.0")
	f.Emission.epoch = 1
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	f.Emission.epoch = 31
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)

	require.NoError(t, f.Keeper.Lambda.Set(f.Ctx, math.LegacyOneDec()))
	require.NoError(t, f.Keeper.LambdaLastUpdatedEpoch.Set(f.Ctx, 1_000_000_000))
	for i := 0; i < 8; i++ {
		_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
		require.NoError(t, err)
	}
	anchorPower, err := f.Keeper.LastPower.Get(f.Ctx, anchor)
	require.NoError(t, err)
	require.Equal(t, int64(1_000_000_000), anchorPower)

	whale := sdk.ValAddress("spike_whale_val_____").String()
	f.Staking.addValidator(t, whale, testPubKey(5), 1, "0.0")
	f.Emission.epoch = 40
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	f.Staking.validators[whale].val.Tokens = math.NewInt(1_000_000_000)
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	if power, powerErr := f.Keeper.LastPower.Get(f.Ctx, whale); powerErr == nil {
		require.Zero(t, power, "tokens bonded in the epoch they arrive do not count yet")
	}

	f.Emission.epoch = 41
	_, err = f.Keeper.RunEndBlock(f.Ctx, f.Emission)
	require.NoError(t, err)
	power, err := f.Keeper.LastPower.Get(f.Ctx, whale)
	require.NoError(t, err)
	anchorPower, err = f.Keeper.LastPower.Get(f.Ctx, anchor)
	require.NoError(t, err)
	share := math.LegacyNewDec(power).Quo(math.LegacyNewDec(power + anchorPower))
	require.False(t, share.GT(types.MaxVotingPowerShiftPerEpoch()), "whale share %s exceeds 1/3", share)
}

func setupTwoCommitteeMembers(t *testing.T, f *testFixture) (sdk.AccAddress, sdk.AccAddress) {
	t.Helper()
	f.Ctx = f.Ctx.WithChainID("orama-devnet-1")
	first := sdk.AccAddress("committee_member_one")
	second := sdk.AccAddress("committee_member_two")
	gs := types.DefaultGenesisState()
	gs.Params.MinCommitteeSize = 1
	gs.Params.BootstrapDeadlineEpochs = 10
	gs.GateSatisfied = true
	gs.BootstrapCommittee = []types.BootstrapMember{
		{OperatorAddress: first.String(), Moniker: "one", ConsensusPubkey: testPubKey(1)},
		{OperatorAddress: second.String(), Moniker: "two", ConsensusPubkey: testPubKey(2)},
	}
	_, err := f.Keeper.InitGenesis(f.Ctx, *gs, f.Emission)
	require.NoError(t, err)
	return first, second
}
