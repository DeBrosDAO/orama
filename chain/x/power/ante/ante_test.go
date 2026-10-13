package ante_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"
	protov2 "google.golang.org/protobuf/proto"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/power/ante"
	"github.com/DeBrosOfficial/network/chain/x/power/keeper"
	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

func TestMain(m *testing.M) {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	cfg.SetBech32PrefixForValidator(params.Bech32PrefixValAddr, params.Bech32PrefixValPub)
	cfg.Seal()
	os.Exit(m.Run())
}

const oramaUnit = int64(params.NoramaPerOrama)

func acc(n byte) sdk.AccAddress { return sdk.AccAddress(repeat(n)) }

func repeat(n byte) []byte {
	b := make([]byte, 20)
	for i := range b {
		b[i] = n
	}
	return b
}

// fakeTx carries messages to a decorator; the decorators read nothing else.
type fakeTx struct{ msgs []sdk.Msg }

func (f fakeTx) GetMsgs() []sdk.Msg                    { return f.msgs }
func (f fakeTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }

// fakeStaking answers the two reads the decorators make.
type fakeStaking struct {
	validators  map[string]stakingtypes.Validator
	delegations map[string]stakingtypes.Delegation
}

func (f fakeStaking) GetValidator(_ context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error) {
	if v, ok := f.validators[addr.String()]; ok {
		return v, nil
	}
	return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
}

func (f fakeStaking) GetDelegation(_ context.Context, del sdk.AccAddress, val sdk.ValAddress) (stakingtypes.Delegation, error) {
	if d, ok := f.delegations[del.String()+"|"+val.String()]; ok {
		return d, nil
	}
	return stakingtypes.Delegation{}, stakingtypes.ErrNoDelegation
}

// world is one validator holding tokens equal to shares (1:1), and the delegations to it.
type world struct {
	ctx     sdk.Context
	power   keeper.Keeper
	staking fakeStaking
	val     sdk.ValAddress
}

// newWorld makes a power keeper on a real store and a validator "val" (the account 1's operator
// address) with 10 ORAMA of tokens, 5 ORAMA of them delegated by account 2 and 5 by account 1 itself.
func newWorld(t *testing.T) *world {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	ctx := testCtx.Ctx.WithBlockHeader(cmtproto.Header{Time: time.Unix(1_700_000_000, 0)})
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	power := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), nil, nil, nil, nil)
	require.NoError(t, power.Params.Set(ctx, types.DefaultParams()))

	val := sdk.ValAddress(acc(1))
	validator := stakingtypes.Validator{
		OperatorAddress: val.String(),
		Tokens:          math.NewInt(10 * oramaUnit),
		DelegatorShares: math.LegacyNewDec(10 * oramaUnit),
	}
	staking := fakeStaking{
		validators: map[string]stakingtypes.Validator{val.String(): validator},
		delegations: map[string]stakingtypes.Delegation{
			acc(2).String() + "|" + val.String(): {DelegatorAddress: acc(2).String(), ValidatorAddress: val.String(), Shares: math.LegacyNewDec(5 * oramaUnit)},
			acc(1).String() + "|" + val.String(): {DelegatorAddress: acc(1).String(), ValidatorAddress: val.String(), Shares: math.LegacyNewDec(5 * oramaUnit)},
		},
	}
	return &world{ctx: ctx, power: power, staking: staking, val: val}
}

func orama(n float64) sdk.Coin {
	return sdk.NewCoin(params.BaseDenom, math.NewInt(int64(n*float64(oramaUnit))))
}

func (w *world) delegate(del sdk.AccAddress, amount sdk.Coin) sdk.Msg {
	return &stakingtypes.MsgDelegate{DelegatorAddress: del.String(), ValidatorAddress: w.val.String(), Amount: amount}
}

func (w *world) undelegate(del sdk.AccAddress, amount sdk.Coin) sdk.Msg {
	return &stakingtypes.MsgUndelegate{DelegatorAddress: del.String(), ValidatorAddress: w.val.String(), Amount: amount}
}

// run sends msgs through decorator and reports the error and whether the next handler ran.
func run(t *testing.T, ctx sdk.Context, d sdk.AnteDecorator, msgs ...sdk.Msg) (error, bool) {
	t.Helper()
	called := false
	_, err := d.AnteHandle(ctx, fakeTx{msgs: msgs}, false, func(c sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
		called = true
		return c, nil
	})
	return err, called
}

func (w *world) minDelegation() sdk.AnteDecorator {
	return ante.NewMinDelegationDecorator(w.staking, w.power)
}

func (w *world) undelegateGuard() sdk.AnteDecorator {
	return ante.NewUndelegateGuard(w.staking, w.power)
}

func requireAccepted(t *testing.T, err error, called bool, why string) {
	t.Helper()
	require.NoError(t, err, why)
	require.True(t, called, "%s: the next handler must run", why)
}

func requireRefused(t *testing.T, err error, called bool, contains string) {
	t.Helper()
	require.ErrorContains(t, err, contains)
	require.False(t, called, "a refused transaction must not reach the next handler")
}

// ---- MinDelegationDecorator ----

func TestMinDelegation_acceptsADelegationAtOrAboveTheMinimum(t *testing.T) {
	w := newWorld(t)
	err, called := run(t, w.ctx, w.minDelegation(), w.delegate(acc(7), orama(1)))
	requireAccepted(t, err, called, "exactly the minimum")
	err, called = run(t, w.ctx, w.minDelegation(), w.delegate(acc(7), orama(3)))
	requireAccepted(t, err, called, "above the minimum")
}

func TestMinDelegation_refusesADelegationBetweenZeroAndTheMinimum(t *testing.T) {
	w := newWorld(t)
	err, called := run(t, w.ctx, w.minDelegation(), w.delegate(acc(7), orama(0.5)))
	requireRefused(t, err, called, "resulting delegation must be at least")
}

func TestMinDelegation_aTopUpCountsTheExistingDelegation(t *testing.T) {
	w := newWorld(t)
	err, called := run(t, w.ctx, w.minDelegation(), w.delegate(acc(2), orama(0.001)))
	requireAccepted(t, err, called, "a delegator with 5 ORAMA already may add a little")
}

func TestMinDelegation_messagesOfOneTransactionApplyInOrder(t *testing.T) {
	w := newWorld(t)
	err, called := run(t, w.ctx, w.minDelegation(), w.delegate(acc(7), orama(0.6)), w.delegate(acc(7), orama(0.6)))
	requireRefused(t, err, called, "resulting delegation must be at least")

	err, called = run(t, w.ctx, w.minDelegation(), w.delegate(acc(7), orama(1)), w.delegate(acc(7), orama(0.1)))
	requireAccepted(t, err, called, "the first message already reaches the minimum")
}

func TestMinDelegation_undelegateMayLeaveZeroOrAtLeastTheMinimumButNothingBetween(t *testing.T) {
	w := newWorld(t)
	err, called := run(t, w.ctx, w.minDelegation(), w.undelegate(acc(2), orama(5)))
	requireAccepted(t, err, called, "a full exit")
	err, called = run(t, w.ctx, w.minDelegation(), w.undelegate(acc(2), orama(4)))
	requireAccepted(t, err, called, "leaving exactly the minimum")
	err, called = run(t, w.ctx, w.minDelegation(), w.undelegate(acc(2), orama(4.5)))
	requireRefused(t, err, called, "delegation left after withdrawal must be at least")
}

func TestMinDelegation_twoUndelegatesApplyTogether(t *testing.T) {
	w := newWorld(t)
	err, called := run(t, w.ctx, w.minDelegation(), w.undelegate(acc(2), orama(2)), w.undelegate(acc(2), orama(2.5)))
	requireRefused(t, err, called, "delegation left after withdrawal must be at least")
}

func TestMinDelegation_undelegateErrors(t *testing.T) {
	w := newWorld(t)
	err, called := run(t, w.ctx, w.minDelegation(), w.undelegate(acc(2), orama(6)))
	requireRefused(t, err, called, "cannot withdraw")
	err, called = run(t, w.ctx, w.minDelegation(), w.undelegate(acc(9), orama(1)))
	requireRefused(t, err, called, "no delegation to withdraw")
	err, called = run(t, w.ctx, w.minDelegation(), w.undelegate(acc(2), sdk.NewCoin(params.BaseDenom, math.ZeroInt())))
	requireRefused(t, err, called, "withdrawal amount must be positive")
}

func TestMinDelegation_unknownValidatorAndBadAddresses(t *testing.T) {
	w := newWorld(t)
	other := sdk.ValAddress(acc(8))
	err, called := run(t, w.ctx, w.minDelegation(), &stakingtypes.MsgDelegate{DelegatorAddress: acc(7).String(), ValidatorAddress: other.String(), Amount: orama(2)})
	requireRefused(t, err, called, "does not exist")

	err, called = run(t, w.ctx, w.minDelegation(), &stakingtypes.MsgDelegate{DelegatorAddress: "nope", ValidatorAddress: w.val.String(), Amount: orama(2)})
	requireRefused(t, err, called, "")
	err, called = run(t, w.ctx, w.minDelegation(), &stakingtypes.MsgDelegate{DelegatorAddress: acc(7).String(), ValidatorAddress: "nope", Amount: orama(2)})
	requireRefused(t, err, called, "")
}

func TestMinDelegation_delegationAmountMustBePositive(t *testing.T) {
	w := newWorld(t)
	err, called := run(t, w.ctx, w.minDelegation(), w.delegate(acc(7), sdk.NewCoin(params.BaseDenom, math.ZeroInt())))
	requireRefused(t, err, called, "delegation amount must be positive")
}

func TestMinDelegation_addressCasingIsOneDelegation(t *testing.T) {
	w := newWorld(t)
	upper := strings.ToUpper(acc(2).String())
	err, called := run(t, w.ctx, w.minDelegation(),
		w.undelegate(acc(2), orama(2)),
		&stakingtypes.MsgUndelegate{DelegatorAddress: upper, ValidatorAddress: w.val.String(), Amount: orama(2.5)},
	)
	requireRefused(t, err, called, "delegation left after withdrawal must be at least")
}

func TestMinDelegation_redelegationChecksBothEnds(t *testing.T) {
	w := newWorld(t)
	dst := sdk.ValAddress(acc(8))
	w.staking.validators[dst.String()] = stakingtypes.Validator{OperatorAddress: dst.String(), Tokens: math.NewInt(4 * oramaUnit), DelegatorShares: math.LegacyNewDec(4 * oramaUnit)}
	redelegate := func(amount sdk.Coin) sdk.Msg {
		return &stakingtypes.MsgBeginRedelegate{DelegatorAddress: acc(2).String(), ValidatorSrcAddress: w.val.String(), ValidatorDstAddress: dst.String(), Amount: amount}
	}
	err, called := run(t, w.ctx, w.minDelegation(), redelegate(orama(2)))
	requireAccepted(t, err, called, "3 ORAMA stay, 2 arrive")
	err, called = run(t, w.ctx, w.minDelegation(), redelegate(orama(4.5)))
	requireRefused(t, err, called, "delegation left after withdrawal must be at least")
}

func TestMinDelegation_aZeroMinimumAndOtherMessagesPassThrough(t *testing.T) {
	w := newWorld(t)
	p := types.DefaultParams()
	p.MinDelegationForRewards = math.ZeroInt()
	require.NoError(t, w.power.Params.Set(w.ctx, p))
	err, called := run(t, w.ctx, w.minDelegation(), w.delegate(acc(7), orama(0.0001)))
	requireAccepted(t, err, called, "the minimum is off")

	require.NoError(t, w.power.Params.Set(w.ctx, types.DefaultParams()))
	err, called = run(t, w.ctx, w.minDelegation(), &stakingtypes.MsgEditValidator{})
	requireAccepted(t, err, called, "a message that moves no delegation")
	err, called = run(t, w.ctx, w.minDelegation())
	requireAccepted(t, err, called, "a transaction with no messages")
}

func TestMinDelegation_createValidatorChecksTheSelfDelegation(t *testing.T) {
	w := newWorld(t)
	newVal := sdk.ValAddress(acc(8))
	create := func(amount sdk.Coin) sdk.Msg {
		return &stakingtypes.MsgCreateValidator{ValidatorAddress: newVal.String(), Value: amount}
	}
	err, called := run(t, w.ctx, w.minDelegation(), create(orama(0.5)))
	requireRefused(t, err, called, "resulting delegation must be at least")
	err, called = run(t, w.ctx, w.minDelegation(), create(orama(2)))
	requireAccepted(t, err, called, "a validator that starts at 2 ORAMA")
}

// ---- UndelegateGuard ----

// committee makes account 1's validator a bootstrap committee member with locked force-bonded stake.
func (w *world) committee(t *testing.T, locked int64, lambda math.LegacyDec) {
	t.Helper()
	require.NoError(t, w.power.BootstrapCommittee.Set(w.ctx, w.val.String(), types.BootstrapMember{OperatorAddress: acc(1).String(), Moniker: "m"}))
	require.NoError(t, w.power.CommitteeSelfBond.Set(w.ctx, w.val.String(), math.NewInt(locked)))
	require.NoError(t, w.power.Lambda.Set(w.ctx, lambda))
}

func TestUndelegateGuard_lockedSelfBondCannotBeWithdrawnWhileTheHandOverRuns(t *testing.T) {
	w := newWorld(t)
	w.committee(t, 3*oramaUnit, math.LegacyNewDecWithPrec(5, 1))

	err, called := run(t, w.ctx, w.undelegateGuard(), w.undelegate(acc(1), orama(2)))
	requireAccepted(t, err, called, "5 - 2 = 3 ORAMA stays, exactly the locked amount")
	err, called = run(t, w.ctx, w.undelegateGuard(), w.undelegate(acc(1), orama(2.5)))
	requireRefused(t, err, called, "force-bonded and locked until lambda reaches 1")
}

func TestUndelegateGuard_halvesAddUp(t *testing.T) {
	w := newWorld(t)
	w.committee(t, 3*oramaUnit, math.LegacyNewDecWithPrec(5, 1))
	err, called := run(t, w.ctx, w.undelegateGuard(), w.undelegate(acc(1), orama(1.2)), w.undelegate(acc(1), orama(1.2)))
	requireRefused(t, err, called, "force-bonded and locked")
}

func TestUndelegateGuard_redelegationOutOfASelfBondIsWithdrawalToo(t *testing.T) {
	w := newWorld(t)
	w.committee(t, 3*oramaUnit, math.LegacyNewDecWithPrec(5, 1))
	dst := sdk.ValAddress(acc(8))
	err, called := run(t, w.ctx, w.undelegateGuard(), &stakingtypes.MsgBeginRedelegate{
		DelegatorAddress: acc(1).String(), ValidatorSrcAddress: w.val.String(), ValidatorDstAddress: dst.String(), Amount: orama(4),
	})
	requireRefused(t, err, called, "force-bonded and locked")
}

func TestUndelegateGuard_unlockedAfterTheHandOverAndForEveryoneElse(t *testing.T) {
	w := newWorld(t)
	w.committee(t, 3*oramaUnit, math.LegacyOneDec())
	err, called := run(t, w.ctx, w.undelegateGuard(), w.undelegate(acc(1), orama(5)))
	requireAccepted(t, err, called, "lambda has reached 1: the seat has lapsed")

	require.NoError(t, w.power.Lambda.Set(w.ctx, math.LegacyNewDecWithPrec(5, 1)))
	err, called = run(t, w.ctx, w.undelegateGuard(), w.undelegate(acc(2), orama(5)))
	requireAccepted(t, err, called, "an outsider's delegation to the committee member's validator is never locked")
}

func TestUndelegateGuard_nonCommitteeValidatorAndNoLockAreNotGuarded(t *testing.T) {
	w := newWorld(t)
	require.NoError(t, w.power.Lambda.Set(w.ctx, math.LegacyNewDecWithPrec(5, 1)))
	err, called := run(t, w.ctx, w.undelegateGuard(), w.undelegate(acc(1), orama(5)))
	requireAccepted(t, err, called, "not a committee member")

	w.committee(t, 0, math.LegacyNewDecWithPrec(5, 1))
	err, called = run(t, w.ctx, w.undelegateGuard(), w.undelegate(acc(1), orama(5)))
	requireAccepted(t, err, called, "a member with nothing force-bonded yet")
}

func TestUndelegateGuard_skipsOtherMessagesAndNonPositiveAmountsAndRefusesBadAddresses(t *testing.T) {
	w := newWorld(t)
	w.committee(t, 3*oramaUnit, math.LegacyNewDecWithPrec(5, 1))
	err, called := run(t, w.ctx, w.undelegateGuard(), w.delegate(acc(1), orama(1)), &stakingtypes.MsgEditValidator{})
	requireAccepted(t, err, called, "messages that withdraw nothing")
	err, called = run(t, w.ctx, w.undelegateGuard(), w.undelegate(acc(1), sdk.NewCoin(params.BaseDenom, math.ZeroInt())))
	requireAccepted(t, err, called, "a zero amount is left to x/staking to refuse")

	err, called = run(t, w.ctx, w.undelegateGuard(), &stakingtypes.MsgUndelegate{DelegatorAddress: "nope", ValidatorAddress: w.val.String(), Amount: orama(1)})
	requireRefused(t, err, called, "")
	err, called = run(t, w.ctx, w.undelegateGuard(), &stakingtypes.MsgUndelegate{DelegatorAddress: acc(1).String(), ValidatorAddress: "nope", Amount: orama(1)})
	requireRefused(t, err, called, "")
}

func TestUndelegateGuard_addressCasingIsOneSelfBond(t *testing.T) {
	w := newWorld(t)
	w.committee(t, 3*oramaUnit, math.LegacyNewDecWithPrec(5, 1))
	err, called := run(t, w.ctx, w.undelegateGuard(),
		w.undelegate(acc(1), orama(1.2)),
		&stakingtypes.MsgUndelegate{DelegatorAddress: strings.ToUpper(acc(1).String()), ValidatorAddress: w.val.String(), Amount: orama(1.2)},
	)
	requireRefused(t, err, called, "force-bonded and locked")
}

func TestUndelegateGuard_aCommitteeMemberWhoseValidatorIsMissingIsAnError(t *testing.T) {
	w := newWorld(t)
	w.committee(t, 3*oramaUnit, math.LegacyNewDecWithPrec(5, 1))
	delete(w.staking.validators, w.val.String())
	err, called := run(t, w.ctx, w.undelegateGuard(), w.undelegate(acc(1), orama(1)))
	requireRefused(t, err, called, "failed to load validator")
}
