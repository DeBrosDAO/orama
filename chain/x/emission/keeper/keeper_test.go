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
	"github.com/DeBrosOfficial/network/chain/x/emission/keeper"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
)

// testBondedPoolAddr stands in for x/staking's real bonded-pool module account address in every
// test in this package.
var testBondedPoolAddr = sdk.AccAddress("test_bonded_pool_addr")

// testFeeCollectorName is an arbitrary module account name used only by
// TestInitGenesis_reconcileBurnsAfterASlashKeepsInvariantHolding to simulate an x/slashing-style
// burn from some other module's account; ReconcileBurns only looks at total supply, so which
// module name is used does not matter.
const testFeeCollectorName = "fee_collector"

// fakeBankKeeper is a minimal, hand-written stand-in for x/bank: a single-denom ledger of
// balances (keyed by module name or, for testBondedPoolAddr, its bech32 string) plus a running
// total supply. It implements exactly the emissiontypes.BankKeeper interface x/emission depends
// on.
type fakeBankKeeper struct {
	balances map[string]math.Int
	supply   math.Int
}

func newFakeBankKeeper() *fakeBankKeeper {
	return &fakeBankKeeper{
		balances: make(map[string]math.Int),
		supply:   math.ZeroInt(),
	}
}

func (b *fakeBankKeeper) MintCoins(_ context.Context, moduleName string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	b.balances[moduleName] = b.balanceOf(moduleName).Add(amount)
	b.supply = b.supply.Add(amount)
	return nil
}

func (b *fakeBankKeeper) SendCoinsFromModuleToModule(_ context.Context, senderModule, recipientModule string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	b.balances[senderModule] = b.balanceOf(senderModule).Sub(amount)
	b.balances[recipientModule] = b.balanceOf(recipientModule).Add(amount)
	return nil
}

func (b *fakeBankKeeper) GetSupply(_ context.Context, denom string) sdk.Coin {
	if denom != params.BaseDenom {
		return sdk.NewCoin(denom, math.ZeroInt())
	}
	return sdk.NewCoin(denom, b.supply)
}

func (b *fakeBankKeeper) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	if denom != params.BaseDenom {
		return sdk.NewCoin(denom, math.ZeroInt())
	}
	return sdk.NewCoin(denom, b.balanceOf(addr.String()))
}

// SetGenesisSupply sets total supply directly, without attributing it to any particular account.
// Used by tests that aren't exercising the bonded-pool premine gate itself (e.g. a re-imported
// genesis, or a deliberately-broken invariant fixture) and just need bank supply to be some
// specific number.
func (b *fakeBankKeeper) SetGenesisSupply(amount math.Int) {
	b.supply = amount
}

// FundBondedPool credits testBondedPoolAddr directly and increases total supply, simulating
// genesis accounts that self-delegated (and so had their funds moved into the bonded pool) before
// x/emission's InitGenesis runs - the devnet-only bootstrap-stake exception.
func (b *fakeBankKeeper) FundBondedPool(amount math.Int) {
	b.balances[testBondedPoolAddr.String()] = b.balanceOf(testBondedPoolAddr.String()).Add(amount)
	b.supply = b.supply.Add(amount)
}

// SimulateExternalBurn decreases moduleName's balance and total supply directly, without going
// through x/emission at all - modeling what x/slashing's BurnCoins does when it slashes a
// validator's bonded or not-bonded stake.
func (b *fakeBankKeeper) SimulateExternalBurn(moduleName string, amount math.Int) {
	b.balances[moduleName] = b.balanceOf(moduleName).Sub(amount)
	b.supply = b.supply.Sub(amount)
}

func (b *fakeBankKeeper) balanceOf(key string) math.Int {
	if v, ok := b.balances[key]; ok {
		return v
	}
	return math.ZeroInt()
}

// fakePowerKeeper stands in for x/power in x/emission's own tests: x/emission only needs to know
// that its minted validator share was handed off successfully, never how x/power's capped-power
// distribution itself works (that is covered by x/power's own tests).
type fakePowerKeeper struct {
	distributed math.Int
	calls       int
}

func newFakePowerKeeper() *fakePowerKeeper {
	return &fakePowerKeeper{distributed: math.ZeroInt()}
}

func (p *fakePowerKeeper) DistributeEpochRewards(_ sdk.Context, _ powertypes.EmissionKeeper, _ string, totalMint math.Int) (math.Int, error) {
	p.calls++
	p.distributed = p.distributed.Add(totalMint)
	return totalMint, nil
}

// testFixture bundles everything a keeper test needs: a live keeper over an in-memory store, and
// the fake bank keeper backing it so tests can inspect minted amounts and supply directly.
type testFixture struct {
	Ctx    sdk.Context
	Keeper keeper.Keeper
	Bank   *fakeBankKeeper
	Power  *fakePowerKeeper
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()

	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	ctx := testCtx.Ctx.
		WithBlockHeader(cmtproto.Header{Time: time.Unix(1_700_000_000, 0)}).
		WithChainID("orama-localnet-keepertest")

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(interfaceRegistry)

	bank := newFakeBankKeeper()
	power := newFakePowerKeeper()
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), bank, power, testBondedPoolAddr)

	return &testFixture{Ctx: ctx, Keeper: k, Bank: bank, Power: power}
}

// initGenesis is a small helper most tests use to get the keeper into a ready state without
// repeating the same lines everywhere. It requires InitGenesis to succeed - tests of InitGenesis
// failure paths call f.Keeper.InitGenesis directly instead.
func (f *testFixture) initGenesis(t *testing.T, mutate func(*types.GenesisState)) {
	t.Helper()
	gs := types.DefaultGenesisState()
	if mutate != nil {
		mutate(gs)
	}
	require.NoError(t, f.Keeper.InitGenesis(f.Ctx, *gs))
}
