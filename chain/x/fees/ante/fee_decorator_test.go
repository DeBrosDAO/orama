package ante_test

import (
	"context"
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
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	feesante "github.com/DeBrosOfficial/network/chain/x/fees/ante"
	"github.com/DeBrosOfficial/network/chain/x/fees/keeper"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// --- fakes: the same shapes x/fees/keeper's own tests use, plus the small AccountKeeper/
// StakingKeeper subsets FeeDecorator itself needs. ---

type fakeBankKeeper struct {
	balances map[string]math.Int
	burned   math.Int
}

func newFakeBankKeeper() *fakeBankKeeper {
	return &fakeBankKeeper{balances: make(map[string]math.Int), burned: math.ZeroInt()}
}

func (b *fakeBankKeeper) balanceOf(key string) math.Int {
	if v, ok := b.balances[key]; ok {
		return v
	}
	return math.ZeroInt()
}

func (b *fakeBankKeeper) fund(key string, amt math.Int) { b.balances[key] = b.balanceOf(key).Add(amt) }

func (b *fakeBankKeeper) SendCoinsFromAccountToModule(_ context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	b.balances[senderAddr.String()] = b.balanceOf(senderAddr.String()).Sub(amount)
	b.balances[recipientModule] = b.balanceOf(recipientModule).Add(amount)
	return nil
}

func (b *fakeBankKeeper) SendCoinsFromModuleToModule(_ context.Context, senderModule, recipientModule string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	b.balances[senderModule] = b.balanceOf(senderModule).Sub(amount)
	b.balances[recipientModule] = b.balanceOf(recipientModule).Add(amount)
	return nil
}

func (b *fakeBankKeeper) SendCoinsFromModuleToAccount(_ context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	b.balances[senderModule] = b.balanceOf(senderModule).Sub(amount)
	b.balances[recipientAddr.String()] = b.balanceOf(recipientAddr.String()).Add(amount)
	return nil
}

func (b *fakeBankKeeper) BurnCoins(_ context.Context, moduleName string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	b.balances[moduleName] = b.balanceOf(moduleName).Sub(amount)
	b.burned = b.burned.Add(amount)
	return nil
}

func (b *fakeBankKeeper) SpendableCoins(_ context.Context, addr sdk.AccAddress) sdk.Coins {
	return sdk.NewCoins(sdk.NewCoin(params.BaseDenom, b.balanceOf(addr.String())))
}

func (b *fakeBankKeeper) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	if v, ok := b.balances[addr.String()]; ok {
		return sdk.NewCoin(denom, v)
	}
	for _, name := range []string{types.ModuleName, types.DepositsModuleName} {
		if authtypes.NewModuleAddress(name).Equals(addr) {
			return sdk.NewCoin(denom, b.balanceOf(name))
		}
	}
	return sdk.NewCoin(denom, math.ZeroInt())
}

// fakeAccountKeeper always reports every address as an existing, empty account: this decorator
// only cares whether the payer exists at all.
type fakeAccountKeeper struct{}

func (fakeAccountKeeper) GetAccount(_ context.Context, addr sdk.AccAddress) sdk.AccountI {
	return authtypes.NewBaseAccount(addr, nil, 0, 0)
}

// fakeStakingKeeper never resolves a proposer: irrelevant to the simulate-mode test below, since
// simulateSettle swallows every error, including this one.
type fakeStakingKeeper struct{}

func (fakeStakingKeeper) GetValidatorByConsAddr(_ context.Context, _ sdk.ConsAddress) (stakingtypes.Validator, error) {
	return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
}

// fakeFeeTx is a minimal hand-written sdk.FeeTx, standing in for a real signed transaction: this
// decorator only ever reads GetFee/GetGas/FeePayer/FeeGranter from it.
type fakeFeeTx struct {
	fee        sdk.Coins
	gas        uint64
	feePayer   sdk.AccAddress
	feeGranter sdk.AccAddress
}

func (t fakeFeeTx) GetMsgs() []sdk.Msg                    { return nil }
func (t fakeFeeTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }
func (t fakeFeeTx) GetGas() uint64                        { return t.gas }
func (t fakeFeeTx) GetFee() sdk.Coins                     { return t.fee }
func (t fakeFeeTx) FeePayer() []byte                      { return t.feePayer }
func (t fakeFeeTx) FeeGranter() []byte                    { return t.feeGranter }

func newTestDecorator(t *testing.T) (feesante.FeeDecorator, sdk.Context, *fakeBankKeeper) {
	t.Helper()

	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	ctx := testCtx.Ctx.WithBlockHeader(cmtproto.Header{Time: time.Unix(1_700_000_000, 0), Height: 10})

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(interfaceRegistry)

	bank := newFakeBankKeeper()
	feesKeeper := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), bank)
	require.NoError(t, feesKeeper.InitGenesis(ctx, *types.DefaultGenesisState()))

	decorator := feesante.NewFeeDecorator(fakeAccountKeeper{}, nil, fakeStakingKeeper{}, feesKeeper)
	return decorator, ctx, bank
}

// TestFeeDecorator_simulateSkipsValidatorMinGasPriceCheck reproduces the RootWallet review finding:
// a `--gas auto` simulation submits an empty fee and gas 0 to estimate what the real fee should be,
// and must never be rejected by this validator's own local minimum-gas-prices mempool policy (that
// check exists to protect CheckTx admission, not simulation - see checkValidatorMinGasPrice's doc
// comment) or by x/fees' base-fee-vs-declared-fee check further down, both of which would otherwise
// fail immediately on a zero/empty declared fee.
func TestFeeDecorator_simulateSkipsValidatorMinGasPriceCheck(t *testing.T) {
	decorator, ctx, _ := newTestDecorator(t)

	// A validator configured with a non-zero local minimum-gas-prices policy, exactly like a real
	// node's app.toml - this is what made the simulate call fail before the fix.
	ctx = ctx.WithIsCheckTx(true).WithMinGasPrices(sdk.NewDecCoins(sdk.NewDecCoinFromDec(params.BaseDenom, math.LegacyNewDecWithPrec(1, 2))))

	tx := fakeFeeTx{
		fee:      sdk.NewCoins(), // empty: exactly what a `--gas auto` estimate submits
		gas:      0,
		feePayer: sdk.AccAddress("simulating_payer_____"),
	}

	nextCalled := false
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
		nextCalled = true
		return ctx, nil
	}

	_, err := decorator.AnteHandle(ctx, tx, true /* simulate */, next)
	require.NoError(t, err, "a simulation must never fail on the local min-gas-price policy or the base fee")
	require.True(t, nextCalled, "the ante chain must still proceed to the next decorator during simulation")
}

// TestFeeDecorator_nonSimulateStillEnforcesValidatorMinGasPrice confirms the fix does not weaken
// real (non-simulate) CheckTx admission: the same empty fee against the same min-gas-price policy
// must still be rejected once simulate is false.
func TestFeeDecorator_nonSimulateStillEnforcesValidatorMinGasPrice(t *testing.T) {
	decorator, ctx, _ := newTestDecorator(t)
	ctx = ctx.WithIsCheckTx(true).WithMinGasPrices(sdk.NewDecCoins(sdk.NewDecCoinFromDec(params.BaseDenom, math.LegacyNewDecWithPrec(1, 2))))

	tx := fakeFeeTx{
		fee:      sdk.NewCoins(),
		gas:      100_000,
		feePayer: sdk.AccAddress("real_tx_payer________"),
	}

	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

	_, err := decorator.AnteHandle(ctx, tx, false /* simulate */, next)
	require.Error(t, err)
	require.Contains(t, err.Error(), "insufficient fees")
}
