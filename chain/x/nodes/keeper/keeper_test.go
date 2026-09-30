package keeper_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	secp256k1 "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

const testChainID = "orama-test"

func TestMain(m *testing.M) {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	cfg.SetBech32PrefixForValidator(params.Bech32PrefixValAddr, params.Bech32PrefixValPub)
	cfg.SetBech32PrefixForConsensusNode(params.Bech32PrefixConsAddr, params.Bech32PrefixConsPub)
	cfg.SetCoinType(params.CoinType)
	os.Exit(m.Run())
}

type fakeBankKeeper struct {
	balances map[string]math.Int
	burned   math.Int
	// refuseTo makes SendCoinsFromModuleToAccount fail for this recipient address.
	refuseTo string
	// refuseWith, when set, is the error the refused transfer fails with.
	refuseWith error
}

func newFakeBankKeeper() *fakeBankKeeper {
	return &fakeBankKeeper{balances: map[string]math.Int{}, burned: math.ZeroInt()}
}

func (b *fakeBankKeeper) balanceOf(key string) math.Int {
	if v, ok := b.balances[key]; ok && !v.IsNil() {
		return v
	}
	return math.ZeroInt()
}

func (b *fakeBankKeeper) fund(key string, amt math.Int) {
	b.balances[key] = b.balanceOf(key).Add(amt)
}

func (b *fakeBankKeeper) sub(key string, amt math.Int) error {
	have := b.balanceOf(key)
	if have.LT(amt) {
		return errInsufficient(key, have, amt)
	}
	next := have.Sub(amt)
	if next.IsZero() {
		delete(b.balances, key)
		return nil
	}
	b.balances[key] = next
	return nil
}

func errInsufficient(key string, have, amt math.Int) error {
	return &insufficientFunds{key: key, have: have, need: amt}
}

type insufficientFunds struct {
	key        string
	have, need math.Int
}

func (e *insufficientFunds) Error() string {
	return "insufficient funds for " + e.key + ": have " + e.have.String() + " need " + e.need.String()
}

func coinAmount(amt sdk.Coins) (math.Int, error) {
	if len(amt) != 1 || amt[0].Denom != params.BaseDenom || !amt[0].Amount.IsPositive() {
		return math.Int{}, errInsufficient(amt.String(), math.ZeroInt(), math.ZeroInt())
	}
	return amt[0].Amount, nil
}

func (b *fakeBankKeeper) SendCoinsFromAccountToModule(_ context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	amount, err := coinAmount(amt)
	if err != nil {
		return err
	}
	if err := b.sub(senderAddr.String(), amount); err != nil {
		return err
	}
	b.fund(recipientModule, amount)
	return nil
}

func (b *fakeBankKeeper) SendCoinsFromModuleToAccount(_ context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	if b.refuseTo != "" && b.refuseTo == recipientAddr.String() {
		if b.refuseWith != nil {
			return b.refuseWith
		}
		return fmt.Errorf("%s is a blocked recipient", recipientAddr)
	}
	amount, err := coinAmount(amt)
	if err != nil {
		return err
	}
	if err := b.sub(senderModule, amount); err != nil {
		return err
	}
	b.fund(recipientAddr.String(), amount)
	return nil
}

func (b *fakeBankKeeper) BurnCoins(_ context.Context, moduleName string, amt sdk.Coins) error {
	amount, err := coinAmount(amt)
	if err != nil {
		return err
	}
	if err := b.sub(moduleName, amount); err != nil {
		return err
	}
	b.burned = b.burned.Add(amount)
	return nil
}

func (b *fakeBankKeeper) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	if v, ok := b.balances[addr.String()]; ok {
		return sdk.NewCoin(denom, v)
	}
	for _, name := range []string{types.ModuleName, "fees_deposits"} {
		if authtypes.NewModuleAddress(name).Equals(addr) {
			return sdk.NewCoin(denom, b.balanceOf(name))
		}
	}
	return sdk.NewCoin(denom, math.ZeroInt())
}

type lockedDeposit struct {
	owner  sdk.AccAddress
	amount math.Int
}

type fakeDeposits struct {
	bank   *fakeBankKeeper
	locked map[string]lockedDeposit
}

func newFakeDeposits(bank *fakeBankKeeper) *fakeDeposits {
	return &fakeDeposits{bank: bank, locked: map[string]lockedDeposit{}}
}

func (f *fakeDeposits) LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error {
	if !amount.IsPositive() {
		return errInsufficient(id, math.ZeroInt(), amount)
	}
	if _, ok := f.locked[id]; ok {
		return errInsufficient(id, amount, amount)
	}
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))
	if err := f.bank.SendCoinsFromAccountToModule(ctx, owner, "fees_deposits", coins); err != nil {
		return err
	}
	f.locked[id] = lockedDeposit{owner: owner, amount: amount}
	return nil
}

func (f *fakeDeposits) ReleaseDeposit(ctx context.Context, id string) (math.Int, math.Int, error) {
	dep, ok := f.locked[id]
	if !ok {
		return math.Int{}, math.Int{}, errInsufficient(id, math.ZeroInt(), math.OneInt())
	}
	delete(f.locked, id)
	burn := dep.amount.QuoRaw(100)
	refund := dep.amount.Sub(burn)
	if refund.IsPositive() {
		coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, refund))
		if err := f.bank.SendCoinsFromModuleToAccount(ctx, "fees_deposits", dep.owner, coins); err != nil {
			return math.Int{}, math.Int{}, err
		}
	}
	if burn.IsPositive() {
		coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, burn))
		if err := f.bank.BurnCoins(ctx, "fees_deposits", coins); err != nil {
			return math.Int{}, math.Int{}, err
		}
	}
	return refund, burn, nil
}

// fakeEarnings is an in-memory earnings ledger with the x/fees semantics x/nodes relies on:
// FundFeeBalance moves earnings into a fee-only balance that nothing else can spend, and
// FundSpendFromEarnings tops the bank balance up from earnings.
type fakeEarnings struct {
	bank     *fakeBankKeeper
	balances map[string]math.Int
	feeOnly  map[string]math.Int
}

func newFakeEarnings(bank *fakeBankKeeper) *fakeEarnings {
	return &fakeEarnings{bank: bank, balances: map[string]math.Int{}, feeOnly: map[string]math.Int{}}
}

func (e *fakeEarnings) balanceOf(addr sdk.AccAddress) math.Int {
	if v, ok := e.balances[addr.String()]; ok {
		return v
	}
	return math.ZeroInt()
}

func (e *fakeEarnings) feeBalanceOf(addr sdk.AccAddress) math.Int {
	if v, ok := e.feeOnly[addr.String()]; ok {
		return v
	}
	return math.ZeroInt()
}

func (e *fakeEarnings) FundFeeBalance(_ context.Context, from, to sdk.AccAddress, amount math.Int) error {
	if !amount.IsPositive() || e.balanceOf(from).LT(amount) {
		return errInsufficient(from.String(), e.balanceOf(from), amount)
	}
	e.balances[from.String()] = e.balanceOf(from).Sub(amount)
	e.feeOnly[to.String()] = e.feeBalanceOf(to).Add(amount)
	return nil
}

func (e *fakeEarnings) FundSpendFromEarnings(_ context.Context, addr sdk.AccAddress, _ string, needed math.Int) error {
	if !needed.IsPositive() || e.bank.balanceOf(addr.String()).GTE(needed) {
		return nil
	}
	shortfall := needed.Sub(e.bank.balanceOf(addr.String()))
	if e.balanceOf(addr).LT(shortfall) {
		return nil
	}
	e.balances[addr.String()] = e.balanceOf(addr).Sub(shortfall)
	e.bank.fund(addr.String(), shortfall)
	return nil
}

type testFixture struct {
	Ctx      sdk.Context
	Keeper   keeper.Keeper
	Bank     *fakeBankKeeper
	Deposits *fakeDeposits
	Earnings *fakeEarnings
	Accounts *fakeAccounts
	Msg      types.MsgServer
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	f := newRawFixture(t)
	gs := types.DefaultGenesisState()
	// The identity lock has its own tests; everywhere else a node's identity counts at once.
	gs.Params.NetworkIdentityLockSeconds = 0
	require.NoError(t, f.Keeper.InitGenesis(f.Ctx, *gs))
	return f
}

func newRawFixture(t *testing.T) *testFixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	header := cmtproto.Header{Time: time.Unix(1_700_000_000, 0).UTC(), ChainID: testChainID, Height: 1}
	ctx := testCtx.Ctx.WithBlockHeader(header).WithChainID(testChainID)
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	bank := newFakeBankKeeper()
	deps := newFakeDeposits(bank)
	earn := newFakeEarnings(bank)
	accounts := newFakeAccounts()
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), bank, deps, earn, accounts)
	return &testFixture{Ctx: ctx, Keeper: k, Bank: bank, Deposits: deps, Earnings: earn, Accounts: accounts, Msg: keeper.NewMsgServerImpl(k)}
}

func (f *testFixture) fund(addr sdk.AccAddress, orama int64) {
	f.Bank.fund(addr.String(), math.NewInt(orama).MulRaw(params.NoramaPerOrama))
}

func (f *testFixture) requireInvariants(t *testing.T) {
	t.Helper()
	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, got.BalanceMatches, got.Detail)
	require.True(t, got.ActiveRolesBonded, got.Detail)
	require.True(t, got.CapacityBacked, got.Detail)
}

// testKeys remembers the private key behind every account newAccount made, so a test that names an
// account as a node's hot key can produce the hot key's own signed binding.
var testKeys sync.Map

func newAccount(t *testing.T) sdk.AccAddress {
	t.Helper()
	priv := secp256k1.GenPrivKey()
	addr := sdk.AccAddress(priv.PubKey().Address())
	require.Contains(t, addr.String(), "orama1")
	testKeys.Store(addr.String(), priv)
	return addr
}

// hotBinding is the "hot-key" binding the hot account signs over the operator and chain.
func hotBinding(t *testing.T, chainID string, operator, hot sdk.AccAddress) types.Binding {
	t.Helper()
	v, ok := testKeys.Load(hot.String())
	require.True(t, ok, "hot key %s was not made by newAccount", hot)
	priv := v.(*secp256k1.PrivKey)
	pub := priv.PubKey().Bytes()
	sig, err := priv.Sign(types.BindingSignBytes(chainID, operator.String(), types.HotKeyService, pub))
	require.NoError(t, err)
	return types.Binding{Service: types.HotKeyService, KeyType: types.KeyTypeSecp256k1, Pubkey: pub, Signature: sig}
}

// withHot appends the hot key's own binding to bindings.
func withHot(t *testing.T, operator, hot sdk.AccAddress, bindings ...types.Binding) []types.Binding {
	t.Helper()
	return append(append([]types.Binding(nil), bindings...), hotBinding(t, testChainID, operator, hot))
}

func secpBinding(t *testing.T, chainID, operator, service string) types.Binding {
	t.Helper()
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey().Bytes()
	sig, err := priv.Sign(types.BindingSignBytes(chainID, operator, service, pub))
	require.NoError(t, err)
	return types.Binding{Service: service, KeyType: types.KeyTypeSecp256k1, Pubkey: pub, Signature: sig}
}

func edBinding(t *testing.T, chainID, operator, service string) types.Binding {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sig := ed25519.Sign(priv, types.BindingSignBytes(chainID, operator, service, pub))
	return types.Binding{Service: service, KeyType: types.KeyTypeEd25519, Pubkey: pub, Signature: sig}
}

func orama(n int64) math.Int {
	return math.NewInt(n).MulRaw(params.NoramaPerOrama)
}

func (f *testFixture) registerOperator(t *testing.T, op sdk.AccAddress) {
	t.Helper()
	_, err := f.Msg.RegisterOperator(f.Ctx, &types.MsgRegisterOperator{Operator: op.String()})
	require.NoError(t, err)
}

func (f *testFixture) registerNode(t *testing.T, op, hot sdk.AccAddress, id string, roles []types.Role, bindings []types.Binding) {
	t.Helper()
	_, err := f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator:   op.String(),
		NodeId:     id,
		Roles:      roles,
		HotKey:     hot.String(),
		Bindings:   withHot(t, op, hot, bindings...),
		Endpoints:  []string{"https://node.example:443"},
		RegionHint: "eu-1",
	})
	require.NoError(t, err)
}
