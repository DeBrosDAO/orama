package keeper_test

import (
	"bytes"
	"context"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	cnftkeeper "github.com/DeBrosOfficial/network/chain/x/cnft/keeper"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	"github.com/DeBrosOfficial/network/chain/x/market/keeper"
	"github.com/DeBrosOfficial/network/chain/x/market/types"
)

func init() {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
}

type fakeBank struct {
	bal map[string]math.Int
}

func newFakeBank() *fakeBank {
	return &fakeBank{bal: map[string]math.Int{}}
}

func (b *fakeBank) get(key string) math.Int {
	v, ok := b.bal[key]
	if !ok || v.IsNil() {
		return math.ZeroInt()
	}
	return v
}

func (b *fakeBank) add(key string, amt math.Int) { b.bal[key] = b.get(key).Add(amt) }

func (b *fakeBank) sub(key string, amt math.Int) error {
	if b.get(key).LT(amt) {
		return errInsufficient
	}
	b.bal[key] = b.get(key).Sub(amt)
	return nil
}

func userKey(addr sdk.AccAddress) string { return "user:" + addr.String() }
func modKey(name string) string          { return "mod:" + name }

func (b *fakeBank) SendCoinsFromAccountToModule(_ context.Context, from sdk.AccAddress, module string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	if err := b.sub(userKey(from), amount); err != nil {
		return err
	}
	b.add(modKey(module), amount)
	return nil
}

func (b *fakeBank) SendCoinsFromModuleToAccount(_ context.Context, module string, to sdk.AccAddress, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	if err := b.sub(modKey(module), amount); err != nil {
		return err
	}
	b.add(userKey(to), amount)
	return nil
}

func (b *fakeBank) SendCoinsFromModuleToModule(_ context.Context, from, to string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	if err := b.sub(modKey(from), amount); err != nil {
		return err
	}
	b.add(modKey(to), amount)
	return nil
}

func (b *fakeBank) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewCoin(denom, b.get(userKey(addr)))
}

type fakeFees struct{}

func (fakeFees) LockDeposit(context.Context, sdk.AccAddress, string, math.Int) error { return nil }

type ledgerEarnings struct {
	bank   *fakeBank
	ledger map[string]math.Int
	calls  int
}

func (e *ledgerEarnings) CreditEarnings(_ context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error {
	e.calls++
	if !amt.IsPositive() {
		return nil
	}
	if err := e.bank.SendCoinsFromModuleToModule(context.Background(), senderModule, "fees", sdk.NewCoins(amt)); err != nil {
		return err
	}
	e.ledger[addr.String()] = e.get(addr).Add(amt.Amount)
	return nil
}

func (e *ledgerEarnings) get(addr sdk.AccAddress) math.Int {
	v, ok := e.ledger[addr.String()]
	if !ok || v.IsNil() {
		return math.ZeroInt()
	}
	return v
}

type errString string

func (e errString) Error() string { return string(e) }

const errInsufficient errString = "insufficient funds"

type fixture struct {
	ctx      sdk.Context
	cnft     cnftkeeper.Keeper
	keeper   keeper.Keeper
	bank     *fakeBank
	earnings *ledgerEarnings
	msg      types.MsgServer
	cnftMsg  cnfttypes.MsgServer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	cnftKey := storetypes.NewKVStoreKey(cnfttypes.StoreKey)
	marketKey := storetypes.NewKVStoreKey(types.StoreKey)
	ctx := testutil.DefaultContextWithKeys(
		map[string]*storetypes.KVStoreKey{cnfttypes.StoreKey: cnftKey, types.StoreKey: marketKey},
		map[string]*storetypes.TransientStoreKey{},
		map[string]*storetypes.MemoryStoreKey{},
	)
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	bank := newFakeBank()
	earnings := &ledgerEarnings{bank: bank, ledger: map[string]math.Int{}}
	cnftK := cnftkeeper.NewKeeper(cdc, runtime.NewKVStoreService(cnftKey), fakeFees{}, earnings)
	require.NoError(t, cnftK.InitGenesis(ctx, *cnfttypes.DefaultGenesisState()))
	marketK := keeper.NewKeeper(cdc, runtime.NewKVStoreService(marketKey), bank, earnings, cnftK)
	require.NoError(t, marketK.InitGenesis(ctx, *types.DefaultGenesisState()))
	return &fixture{
		ctx:      ctx,
		cnft:     cnftK,
		keeper:   marketK,
		bank:     bank,
		earnings: earnings,
		msg:      keeper.NewMsgServer(marketK),
		cnftMsg:  cnftkeeper.NewMsgServer(cnftK),
	}
}

func bech(n byte) string {
	raw := make(sdk.AccAddress, 20)
	for i := range raw {
		raw[i] = n
	}
	return raw.String()
}

func assetID(n byte) []byte { return bytes.Repeat([]byte{n}, cnfttypes.HashSize) }
