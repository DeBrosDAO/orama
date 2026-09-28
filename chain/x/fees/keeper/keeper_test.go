package keeper_test

import (
	"context"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/keeper"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// fakeBankKeeper is a minimal, hand-written stand-in for x/bank, tracking module and account
// balances in a single denom (params.BaseDenom) plus a running burned total.
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

type testFixture struct {
	Ctx    sdk.Context
	Keeper keeper.Keeper
	Bank   *fakeBankKeeper
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()

	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	ctx := testCtx.Ctx.WithBlockHeader(cmtproto.Header{Time: time.Unix(1_700_000_000, 0)})

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(interfaceRegistry)

	bank := newFakeBankKeeper()
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), bank)

	return &testFixture{Ctx: ctx, Keeper: k, Bank: bank}
}

func (f *testFixture) initGenesis(t *testing.T, mutate func(*types.GenesisState)) {
	t.Helper()
	gs := types.DefaultGenesisState()
	if mutate != nil {
		mutate(gs)
	}
	require.NoError(t, f.Keeper.InitGenesis(f.Ctx, *gs))
}
