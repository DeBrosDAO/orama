package keeper_test

import (
	"context"
	"fmt"
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
	"github.com/DeBrosOfficial/network/chain/x/houses/keeper"
	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

func acc(n int) sdk.AccAddress {
	raw := make([]byte, 20)
	raw[18] = byte(n >> 8)
	raw[19] = byte(n)
	return sdk.AccAddress(raw)
}

type fakeBank struct {
	balances map[string]math.Int
	burned   math.Int
}

func newFakeBank() *fakeBank {
	return &fakeBank{balances: map[string]math.Int{}, burned: math.ZeroInt()}
}

func (b *fakeBank) balanceOf(key string) math.Int {
	if v, ok := b.balances[key]; ok {
		return v
	}
	return math.ZeroInt()
}

func (b *fakeBank) fund(key string, amt math.Int) {
	b.balances[key] = b.balanceOf(key).Add(amt)
}

func (b *fakeBank) sub(key string, amt math.Int) error {
	have := b.balanceOf(key)
	if have.LT(amt) {
		return fmt.Errorf("insufficient balance for %s", key)
	}
	b.balances[key] = have.Sub(amt)
	return nil
}

func (b *fakeBank) SendCoinsFromAccountToModule(_ context.Context, sender sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	if err := b.sub(sender.String(), amount); err != nil {
		return err
	}
	b.fund(recipientModule, amount)
	return nil
}

func (b *fakeBank) SendCoinsFromModuleToAccount(_ context.Context, senderModule string, recipient sdk.AccAddress, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	if err := b.sub(senderModule, amount); err != nil {
		return err
	}
	b.fund(recipient.String(), amount)
	return nil
}

func (b *fakeBank) BurnCoins(_ context.Context, moduleName string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	if err := b.sub(moduleName, amount); err != nil {
		return err
	}
	b.burned = b.burned.Add(amount)
	return nil
}

func (b *fakeBank) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	if authtypes.NewModuleAddress(types.ModuleName).Equals(addr) {
		return sdk.NewCoin(denom, b.balanceOf(types.ModuleName))
	}
	return sdk.NewCoin(denom, b.balanceOf(addr.String()))
}

type fakeStaking struct {
	total math.Int
	dels  []types.BondedDelegation
}

func (s *fakeStaking) TotalBondedTokens(context.Context) (math.Int, error) {
	if s.total.IsNil() {
		return math.ZeroInt(), nil
	}
	return s.total, nil
}

func (s *fakeStaking) Delegations(context.Context) ([]types.BondedDelegation, error) {
	return s.dels, nil
}

type fakePower struct {
	lambda math.LegacyDec
}

func (p *fakePower) Lambda(context.Context) (math.LegacyDec, error) {
	if p.lambda.IsNil() {
		return math.LegacyZeroDec(), nil
	}
	return p.lambda, nil
}

type fakeOperators struct {
	ops []types.OperatorInfo
}

func (o *fakeOperators) Operators(context.Context) ([]types.OperatorInfo, error) {
	return o.ops, nil
}

type fakeEmission struct {
	ceiling map[uint64]math.Int
	minted  map[uint64]math.Int
	calls   int
}

func newFakeEmission() *fakeEmission {
	return &fakeEmission{ceiling: map[uint64]math.Int{}, minted: map[uint64]math.Int{}}
}

func (e *fakeEmission) MintDevelopmentSpend(_ context.Context, epoch uint64, amount math.Int) (string, error) {
	e.calls++
	if amount.IsNil() || !amount.IsPositive() {
		return "", fmt.Errorf("development spend refused: amount must be positive")
	}
	ceiling := e.ceiling[epoch]
	if ceiling.IsNil() {
		ceiling = math.ZeroInt()
	}
	already := e.minted[epoch]
	if already.IsNil() {
		already = math.ZeroInt()
	}
	if amount.GT(ceiling.Sub(already)) {
		return "", fmt.Errorf("development spend refused: amount %s exceeds remaining ceiling", amount)
	}
	e.minted[epoch] = already.Add(amount)
	return "emission", nil
}

type earned struct {
	module string
	addr   string
	amount math.Int
}

type fakeEarnings struct {
	credits []earned
}

func (e *fakeEarnings) CreditEarnings(_ context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error {
	e.credits = append(e.credits, earned{module: senderModule, addr: addr.String(), amount: amt.Amount})
	return nil
}

type testFixture struct {
	Ctx       sdk.Context
	Keeper    keeper.Keeper
	Bank      *fakeBank
	Staking   *fakeStaking
	Power     *fakePower
	Operators *fakeOperators
	Emission  *fakeEmission
	Earnings  *fakeEarnings
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	ctx := testCtx.Ctx.WithBlockHeader(cmtproto.Header{Time: time.Unix(1_700_000_000, 0), Height: 1})

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(interfaceRegistry)
	cdc := codec.NewProtoCodec(interfaceRegistry)

	bank := newFakeBank()
	staking := &fakeStaking{total: math.ZeroInt()}
	power := &fakePower{lambda: math.LegacyZeroDec()}
	operators := &fakeOperators{}
	emission := newFakeEmission()
	earnings := &fakeEarnings{}
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), bank, staking, power, operators, emission, earnings)
	f := &testFixture{
		Ctx: ctx, Keeper: k, Bank: bank, Staking: staking, Power: power,
		Operators: operators, Emission: emission, Earnings: earnings,
	}
	gs := types.DefaultGenesisState()
	gs.Params.VotingPeriodSeconds = int64((24 * time.Hour) / time.Second)
	require.NoError(t, f.Keeper.InitGenesis(f.Ctx, *gs))
	return f
}

func (f *testFixture) advance(t *testing.T, d time.Duration) {
	t.Helper()
	f.Ctx = f.Ctx.WithBlockTime(f.Ctx.BlockTime().Add(d))
	require.NoError(t, f.Keeper.Advance(f.Ctx))
}

func spreadOperators(n int) []types.OperatorInfo {
	ops := make([]types.OperatorInfo, n)
	for i := 0; i < n; i++ {
		ops[i] = types.OperatorInfo{
			Address:     acc(i + 1),
			Prefix16:    fmt.Sprintf("10.%d.0.0/16", i/3),
			ASN:         uint32((i % 5) + 1),
			ServiceDays: types.MinServiceDays,
		}
	}
	return ops
}

func (f *testFixture) lockOperators(t *testing.T, ops []types.OperatorInfo) {
	t.Helper()
	bond := types.DefaultHouseBond()
	for _, op := range ops {
		f.Bank.fund(op.Address.String(), bond)
		require.NoError(t, f.Keeper.LockHouseBond(f.Ctx, op.Address, bond))
	}
}

func (f *testFixture) seatHouse(t *testing.T, n int) {
	t.Helper()
	ops := spreadOperators(n)
	f.Operators.ops = ops
	f.lockOperators(t, ops)
}

func (f *testFixture) selfBond(addr sdk.AccAddress, amount math.Int) {
	f.Staking.total = amount
	f.Staking.dels = []types.BondedDelegation{{Delegator: addr, Validator: addr, Amount: amount}}
}

func parameterContent() types.ProposalContent {
	p := types.DefaultParams()
	p.VotingPeriodSeconds = int64((24 * time.Hour) / time.Second)
	return types.ProposalContent{ParameterChange: &types.ParameterChange{
		TokenQuorum:            p.TokenQuorum,
		TokenPassThreshold:     p.TokenPassThreshold,
		VotingPeriodSeconds:    p.VotingPeriodSeconds,
		HouseBond:              p.HouseBond,
		MaxEligiblePerPrefix16: p.MaxEligiblePerPrefix16,
		MaxEligiblePerAsn:      p.MaxEligiblePerAsn,
	}}
}

func (f *testFixture) proposal(t *testing.T, id uint64) types.Proposal {
	t.Helper()
	p, err := f.Keeper.Proposals.Get(f.Ctx, id)
	require.NoError(t, err)
	return p
}
