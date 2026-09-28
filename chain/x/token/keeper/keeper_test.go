package keeper_test

import (
	"context"
	"fmt"
	"os"
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
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	"github.com/DeBrosOfficial/network/chain/x/token/keeper"
	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

func TestMain(m *testing.M) {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	cfg.Seal()
	os.Exit(m.Run())
}

func addr(n byte) sdk.AccAddress {
	return sdk.AccAddress(bytesRepeat(n))
}

func bytesRepeat(n byte) []byte {
	b := make([]byte, 20)
	for i := range b {
		b[i] = n
	}
	return b
}

// fakeBank tracks every denom, for accounts and for the module accounts
// x/token and x/fees use. It refuses a debit that would go negative.
type fakeBank struct {
	balances map[string]map[string]math.Int
	supply   map[string]math.Int
	burned   map[string]math.Int
}

func newFakeBank() *fakeBank {
	return &fakeBank{
		balances: map[string]map[string]math.Int{},
		supply:   map[string]math.Int{},
		burned:   map[string]math.Int{},
	}
}

func (b *fakeBank) balanceOf(key, denom string) math.Int {
	if denoms, ok := b.balances[key]; ok {
		if v, ok := denoms[denom]; ok {
			return v
		}
	}
	return math.ZeroInt()
}

func (b *fakeBank) supplyOf(denom string) math.Int {
	if v, ok := b.supply[denom]; ok {
		return v
	}
	return math.ZeroInt()
}

func (b *fakeBank) burnedOf(denom string) math.Int {
	if v, ok := b.burned[denom]; ok {
		return v
	}
	return math.ZeroInt()
}

func (b *fakeBank) fund(key, denom string, amt math.Int) {
	b.credit(key, denom, amt)
	b.supply[denom] = b.supplyOf(denom).Add(amt)
}

func (b *fakeBank) credit(key, denom string, amt math.Int) {
	if _, ok := b.balances[key]; !ok {
		b.balances[key] = map[string]math.Int{}
	}
	b.balances[key][denom] = b.balanceOf(key, denom).Add(amt)
}

func (b *fakeBank) debit(key, denom string, amt math.Int) error {
	have := b.balanceOf(key, denom)
	if have.LT(amt) {
		return fmt.Errorf("insufficient %s balance for %s: have %s, need %s", denom, key, have, amt)
	}
	next := have.Sub(amt)
	if next.IsZero() {
		delete(b.balances[key], denom)
		if len(b.balances[key]) == 0 {
			delete(b.balances, key)
		}
		return nil
	}
	b.balances[key][denom] = next
	return nil
}

func (b *fakeBank) keyOf(addr sdk.AccAddress) string {
	for _, name := range []string{types.ModuleName, feestypes.ModuleName, feestypes.DepositsModuleName} {
		if authtypes.NewModuleAddress(name).Equals(addr) {
			return name
		}
	}
	return addr.String()
}

func requirePositiveCoins(amt sdk.Coins) error {
	if len(amt) == 0 || !amt.IsAllPositive() {
		return fmt.Errorf("amount must be positive, got %s", amt)
	}
	return nil
}

func (b *fakeBank) MintCoins(_ context.Context, moduleName string, amt sdk.Coins) error {
	if err := requirePositiveCoins(amt); err != nil {
		return err
	}
	for _, coin := range amt {
		b.credit(moduleName, coin.Denom, coin.Amount)
		b.supply[coin.Denom] = b.supplyOf(coin.Denom).Add(coin.Amount)
	}
	return nil
}

func (b *fakeBank) BurnCoins(_ context.Context, moduleName string, amt sdk.Coins) error {
	if err := requirePositiveCoins(amt); err != nil {
		return err
	}
	for _, coin := range amt {
		if b.supplyOf(coin.Denom).LT(coin.Amount) {
			return fmt.Errorf("burn would make %s supply negative", coin.Denom)
		}
		if err := b.debit(moduleName, coin.Denom, coin.Amount); err != nil {
			return err
		}
		next := b.supplyOf(coin.Denom).Sub(coin.Amount)
		if next.IsZero() {
			delete(b.supply, coin.Denom)
		} else {
			b.supply[coin.Denom] = next
		}
		b.burned[coin.Denom] = b.burnedOf(coin.Denom).Add(coin.Amount)
	}
	return nil
}

func (b *fakeBank) SendCoins(_ context.Context, fromAddr, toAddr sdk.AccAddress, amt sdk.Coins) error {
	if err := requirePositiveCoins(amt); err != nil {
		return err
	}
	for _, coin := range amt {
		if err := b.debit(b.keyOf(fromAddr), coin.Denom, coin.Amount); err != nil {
			return err
		}
		b.credit(b.keyOf(toAddr), coin.Denom, coin.Amount)
	}
	return nil
}

func (b *fakeBank) SendCoinsFromModuleToAccount(_ context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	if err := requirePositiveCoins(amt); err != nil {
		return err
	}
	for _, coin := range amt {
		if err := b.debit(senderModule, coin.Denom, coin.Amount); err != nil {
			return err
		}
		b.credit(b.keyOf(recipientAddr), coin.Denom, coin.Amount)
	}
	return nil
}

func (b *fakeBank) SendCoinsFromAccountToModule(_ context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	if err := requirePositiveCoins(amt); err != nil {
		return err
	}
	for _, coin := range amt {
		if err := b.debit(b.keyOf(senderAddr), coin.Denom, coin.Amount); err != nil {
			return err
		}
		b.credit(recipientModule, coin.Denom, coin.Amount)
	}
	return nil
}

func (b *fakeBank) SendCoinsFromModuleToModule(_ context.Context, senderModule, recipientModule string, amt sdk.Coins) error {
	if err := requirePositiveCoins(amt); err != nil {
		return err
	}
	for _, coin := range amt {
		if err := b.debit(senderModule, coin.Denom, coin.Amount); err != nil {
			return err
		}
		b.credit(recipientModule, coin.Denom, coin.Amount)
	}
	return nil
}

func (b *fakeBank) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewCoin(denom, b.balanceOf(b.keyOf(addr), denom))
}

func (b *fakeBank) SpendableCoin(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewCoin(denom, b.balanceOf(b.keyOf(addr), denom))
}

func (b *fakeBank) GetSupply(_ context.Context, denom string) sdk.Coin {
	return sdk.NewCoin(denom, b.supplyOf(denom))
}

// fakeFees matches x/fees LockDeposit / ReleaseDeposit: the full amount is
// locked, and release refunds 99% to an earnings balance while burning 1%,
// using x/fees' own SplitDeposit and default params.
type fakeFees struct {
	bank     *fakeBank
	deposits map[string]feestypes.Deposit
	earnings map[string]math.Int
	burned   math.Int
}

func newFakeFees(bank *fakeBank) *fakeFees {
	return &fakeFees{
		bank:     bank,
		deposits: map[string]feestypes.Deposit{},
		earnings: map[string]math.Int{},
		burned:   math.ZeroInt(),
	}
}

func (f *fakeFees) LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error {
	if amount.IsNil() || !amount.IsPositive() {
		return fmt.Errorf("deposit amount must be positive")
	}
	if _, ok := f.deposits[id]; ok {
		return fmt.Errorf("deposit id %q already exists", id)
	}
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))
	if err := f.bank.SendCoinsFromAccountToModule(ctx, owner, feestypes.DepositsModuleName, coins); err != nil {
		return fmt.Errorf("failed to lock deposit %q: %w", id, err)
	}
	f.deposits[id] = feestypes.Deposit{Id: id, Owner: owner.String(), Amount: amount}
	return nil
}

func (f *fakeFees) ReleaseDeposit(ctx context.Context, id string) (math.Int, math.Int, error) {
	deposit, ok := f.deposits[id]
	if !ok {
		return math.Int{}, math.Int{}, fmt.Errorf("deposit id %q does not exist", id)
	}
	refund, burn := feestypes.SplitDeposit(deposit.Amount, feestypes.DefaultParams())
	if burn.IsPositive() {
		coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, burn))
		if err := f.bank.BurnCoins(ctx, feestypes.DepositsModuleName, coins); err != nil {
			return math.Int{}, math.Int{}, fmt.Errorf("failed to burn deposit %q: %w", id, err)
		}
		f.burned = f.burned.Add(burn)
	}
	if refund.IsPositive() {
		coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, refund))
		if err := f.bank.SendCoinsFromModuleToModule(ctx, feestypes.DepositsModuleName, feestypes.ModuleName, coins); err != nil {
			return math.Int{}, math.Int{}, fmt.Errorf("failed to refund deposit %q: %w", id, err)
		}
		prev := math.ZeroInt()
		if v, ok := f.earnings[deposit.Owner]; ok {
			prev = v
		}
		f.earnings[deposit.Owner] = prev.Add(refund)
	}
	delete(f.deposits, id)
	return refund, burn, nil
}

func (f *fakeFees) GetDeposit(_ context.Context, id string) (feestypes.Deposit, error) {
	deposit, ok := f.deposits[id]
	if !ok {
		return feestypes.Deposit{}, fmt.Errorf("deposit id %q does not exist", id)
	}
	return deposit, nil
}

func (f *fakeFees) earningsOf(owner string) math.Int {
	if v, ok := f.earnings[owner]; ok {
		return v
	}
	return math.ZeroInt()
}

type testFixture struct {
	Ctx    sdk.Context
	Keeper keeper.Keeper
	Bank   *fakeBank
	Fees   *fakeFees
}

func newTestFixture(t *testing.T, hook types.TransferHook) *testFixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	ctx := testCtx.Ctx.WithBlockHeader(cmtproto.Header{Time: time.Unix(1_700_000_000, 0)})

	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	bank := newFakeBank()
	fees := newFakeFees(bank)
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), bank, fees, hook)
	return &testFixture{Ctx: ctx, Keeper: k, Bank: bank, Fees: fees}
}

func (f *testFixture) initGenesis(t *testing.T, mutate func(*types.GenesisState)) {
	t.Helper()
	gs := types.DefaultGenesisState()
	if mutate != nil {
		mutate(gs)
	}
	require.NoError(t, f.Keeper.InitGenesis(f.Ctx, *gs))
}

func (f *testFixture) fund(key, denom string, amt math.Int) {
	f.Bank.fund(key, denom, amt)
}

func (f *testFixture) requireInvariants(t *testing.T) {
	t.Helper()
	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, got.SupplyMatches, got.Detail)
	require.True(t, got.DepositsMatch, got.Detail)
}

type hookFunc func(ctx context.Context, denom string, from, to sdk.AccAddress, amount math.Int) error

func (h hookFunc) OnTransfer(ctx context.Context, denom string, from, to sdk.AccAddress, amount math.Int) error {
	if h == nil {
		return nil
	}
	return h(ctx, denom, from, to, amount)
}
