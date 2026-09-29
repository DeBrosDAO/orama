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
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/power/keeper"
	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// --- fake StakingKeeper ---

type fakeDelegation struct {
	delegator string
	shares    math.LegacyDec
}

type fakeValidator struct {
	val         stakingtypes.Validator
	delegations []fakeDelegation
	// powerIndexed mirrors the real staking keeper's separate power-index store (see
	// GetBondedValidatorsByPower's doc comment in x/power/types.StakingKeeper): only a validator
	// that has actually been delegated to (via Delegate) is power-indexed, so a committee member
	// with a bare (zero-token) InitGenesis-created record - set only via SetValidator, never
	// Delegate - must NOT appear as a GetBondedValidatorsByPower "outsider" on the strength of that
	// record alone.
	powerIndexed bool
}

type fakeStakingKeeper struct {
	validators map[string]*fakeValidator // keyed by valoper bech32
	bondDenom  string
}

func newFakeStakingKeeper() *fakeStakingKeeper {
	return &fakeStakingKeeper{validators: make(map[string]*fakeValidator), bondDenom: params.BaseDenom}
}

// addValidator registers a validator with a self-delegation of selfBond tokens.
func (f *fakeStakingKeeper) addValidator(t *testing.T, valoperAddr string, pubKey []byte, tokens int64, commissionRate string) {
	t.Helper()
	pk := &ed25519.PubKey{Key: pubKey}
	pkAny, err := codectypes.NewAnyWithValue(pk)
	require.NoError(t, err)
	v := stakingtypes.Validator{
		OperatorAddress: valoperAddr,
		ConsensusPubkey: pkAny,
		Status:          stakingtypes.Bonded,
		Tokens:          math.ZeroInt(),
		DelegatorShares: math.LegacyZeroDec(),
		Commission: stakingtypes.Commission{
			CommissionRates: stakingtypes.CommissionRates{Rate: math.LegacyMustNewDecFromStr(commissionRate)},
		},
	}
	f.validators[valoperAddr] = &fakeValidator{val: v}
	if tokens > 0 {
		valAddr, err := sdk.ValAddressFromBech32(valoperAddr)
		require.NoError(t, err)
		delAddr := sdk.AccAddress(valAddr)
		_, err = f.Delegate(context.Background(), delAddr, math.NewInt(tokens), stakingtypes.Unbonded, v, true)
		require.NoError(t, err)
	}
}

func (f *fakeStakingKeeper) GetValidator(_ context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error) {
	fv, ok := f.validators[addr.String()]
	if !ok {
		return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
	}
	return fv.val, nil
}

func (f *fakeStakingKeeper) GetBondedValidatorsByPower(_ context.Context) ([]stakingtypes.Validator, error) {
	// Deterministic order for tests: sorted by operator address.
	var addrs []string
	for addr, fv := range f.validators {
		if fv.val.Status == stakingtypes.Bonded && fv.powerIndexed {
			addrs = append(addrs, addr)
		}
	}
	types.SortAddresses(addrs)
	out := make([]stakingtypes.Validator, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, f.validators[a].val)
	}
	return out, nil
}

func (f *fakeStakingKeeper) GetValidatorDelegations(_ context.Context, valAddr sdk.ValAddress) ([]stakingtypes.Delegation, error) {
	fv, ok := f.validators[valAddr.String()]
	if !ok {
		return nil, nil
	}
	out := make([]stakingtypes.Delegation, 0, len(fv.delegations))
	for _, d := range fv.delegations {
		out = append(out, stakingtypes.Delegation{DelegatorAddress: d.delegator, ValidatorAddress: valAddr.String(), Shares: d.shares})
	}
	return out, nil
}

func (f *fakeStakingKeeper) GetDelegation(_ context.Context, delAddr sdk.AccAddress, valAddr sdk.ValAddress) (stakingtypes.Delegation, error) {
	fv, ok := f.validators[valAddr.String()]
	if !ok {
		return stakingtypes.Delegation{}, stakingtypes.ErrNoDelegation
	}
	for _, d := range fv.delegations {
		if d.delegator == delAddr.String() {
			return stakingtypes.Delegation{DelegatorAddress: d.delegator, ValidatorAddress: valAddr.String(), Shares: d.shares}, nil
		}
	}
	return stakingtypes.Delegation{}, stakingtypes.ErrNoDelegation
}

func (f *fakeStakingKeeper) Delegate(_ context.Context, delAddr sdk.AccAddress, bondAmt math.Int, _ stakingtypes.BondStatus, validator stakingtypes.Validator, _ bool) (math.LegacyDec, error) {
	fv, ok := f.validators[validator.OperatorAddress]
	if !ok {
		return math.LegacyDec{}, stakingtypes.ErrNoValidatorFound
	}
	var newShares math.LegacyDec
	if fv.val.Tokens.IsZero() || fv.val.DelegatorShares.IsZero() {
		newShares = bondAmt.ToLegacyDec()
	} else {
		newShares = fv.val.DelegatorShares.MulInt(bondAmt).QuoInt(fv.val.Tokens)
	}
	fv.val.Tokens = fv.val.Tokens.Add(bondAmt)
	fv.val.DelegatorShares = fv.val.DelegatorShares.Add(newShares)
	fv.powerIndexed = true

	found := false
	for i, d := range fv.delegations {
		if d.delegator == delAddr.String() {
			fv.delegations[i].shares = fv.delegations[i].shares.Add(newShares)
			found = true
			break
		}
	}
	if !found {
		fv.delegations = append(fv.delegations, fakeDelegation{delegator: delAddr.String(), shares: newShares})
	}
	return newShares, nil
}

func (f *fakeStakingKeeper) BondDenom(_ context.Context) (string, error) { return f.bondDenom, nil }

// jail marks a validator jailed and transitions it out of Bonded status and the power index,
// mirroring what a real Jail call does (security review B1/B2/C1 test scenarios).
func (f *fakeStakingKeeper) jail(t *testing.T, valoperAddr string) {
	t.Helper()
	fv, ok := f.validators[valoperAddr]
	require.True(t, ok, "validator %q must exist to jail it", valoperAddr)
	fv.val.Jailed = true
	fv.val.Status = stakingtypes.Unbonding
	fv.powerIndexed = false
}

// setInvalidExRate marks a validator's token/share exchange rate invalid, mirroring a validator
// that has been slashed to zero real tokens while delegator shares remain outstanding (security
// review C1's "InvalidExRate" scenario).
func (f *fakeStakingKeeper) setInvalidExRate(t *testing.T, valoperAddr string) {
	t.Helper()
	fv, ok := f.validators[valoperAddr]
	require.True(t, ok, "validator %q must exist", valoperAddr)
	fv.val.Tokens = math.ZeroInt()
}

// SetValidator and SetValidatorByConsAddr let InitGenesis register a bootstrap committee member's
// validator record directly, exactly as the real staking keeper does (see
// types.StakingKeeper's doc comment).
func (f *fakeStakingKeeper) SetValidator(_ context.Context, validator stakingtypes.Validator) error {
	fv, ok := f.validators[validator.OperatorAddress]
	if !ok {
		fv = &fakeValidator{}
		f.validators[validator.OperatorAddress] = fv
	}
	fv.val = validator
	return nil
}

func (f *fakeStakingKeeper) SetValidatorByConsAddr(_ context.Context, _ stakingtypes.Validator) error {
	// The fake's GetValidator/GetBondedValidatorsByPower/etc. are all keyed by operator address
	// already (see f.validators), so there is no separate cons-addr index to maintain here.
	return nil
}

// Hooks returns a no-op stand-in: this test double never exercises x/slashing's real hook chain,
// only that InitGenesis calls AfterValidatorCreated without erroring.
func (f *fakeStakingKeeper) Hooks() stakingtypes.StakingHooks { return fakeStakingHooks{} }

type fakeStakingHooks struct{}

func (fakeStakingHooks) AfterValidatorCreated(context.Context, sdk.ValAddress) error   { return nil }
func (fakeStakingHooks) BeforeValidatorModified(context.Context, sdk.ValAddress) error { return nil }
func (fakeStakingHooks) AfterValidatorRemoved(context.Context, sdk.ConsAddress, sdk.ValAddress) error {
	return nil
}
func (fakeStakingHooks) AfterValidatorBonded(context.Context, sdk.ConsAddress, sdk.ValAddress) error {
	return nil
}
func (fakeStakingHooks) AfterValidatorBeginUnbonding(context.Context, sdk.ConsAddress, sdk.ValAddress) error {
	return nil
}
func (fakeStakingHooks) BeforeDelegationCreated(context.Context, sdk.AccAddress, sdk.ValAddress) error {
	return nil
}
func (fakeStakingHooks) BeforeDelegationSharesModified(context.Context, sdk.AccAddress, sdk.ValAddress) error {
	return nil
}
func (fakeStakingHooks) BeforeDelegationRemoved(context.Context, sdk.AccAddress, sdk.ValAddress) error {
	return nil
}
func (fakeStakingHooks) AfterDelegationModified(context.Context, sdk.AccAddress, sdk.ValAddress) error {
	return nil
}
func (fakeStakingHooks) BeforeValidatorSlashed(context.Context, sdk.ValAddress, math.LegacyDec) error {
	return nil
}
func (fakeStakingHooks) AfterUnbondingInitiated(context.Context, uint64) error { return nil }

// --- fake SlashingKeeper ---

// fakeSlashingKeeper is a minimal stand-in for x/slashing's keeper: it only tracks which consensus
// addresses have been tombstoned, since that is all types.SlashingKeeper needs (committeeEligible's
// B2 check: a tombstoned committee member's seat is gone for good).
type fakeSlashingKeeper struct {
	tombstoned map[string]bool
}

func newFakeSlashingKeeper() *fakeSlashingKeeper {
	return &fakeSlashingKeeper{tombstoned: make(map[string]bool)}
}

func (s *fakeSlashingKeeper) IsTombstoned(_ context.Context, consAddr sdk.ConsAddress) bool {
	return s.tombstoned[consAddr.String()]
}

// tombstone marks pubKey's consensus address as tombstoned.
func (s *fakeSlashingKeeper) tombstone(pubKey []byte) {
	pk := &ed25519.PubKey{Key: pubKey}
	s.tombstoned[sdk.ConsAddress(pk.Address()).String()] = true
}

// --- fake BankKeeper ---

type fakeBankKeeper struct {
	balances map[string]math.Int // module name or bech32 account string
}

func newFakeBankKeeper() *fakeBankKeeper { return &fakeBankKeeper{balances: make(map[string]math.Int)} }

func (b *fakeBankKeeper) balanceOf(key string) math.Int {
	if v, ok := b.balances[key]; ok {
		return v
	}
	return math.ZeroInt()
}

func (b *fakeBankKeeper) fund(key string, amt math.Int) { b.balances[key] = b.balanceOf(key).Add(amt) }

func (b *fakeBankKeeper) SendCoinsFromModuleToModule(_ context.Context, senderModule, recipientModule string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	b.balances[senderModule] = b.balanceOf(senderModule).Sub(amount)
	b.balances[recipientModule] = b.balanceOf(recipientModule).Add(amount)
	return nil
}

// GetBalance answers for x/power's own module address from the module-name ledger and for any
// other address from the bech32 ledger.
func (b *fakeBankKeeper) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	if addr.Equals(authtypes.NewModuleAddress(types.ModuleName)) {
		return sdk.NewCoin(denom, b.balanceOf(types.ModuleName))
	}
	return sdk.NewCoin(denom, b.balanceOf(addr.String()))
}

func (b *fakeBankKeeper) SendCoinsFromModuleToAccount(_ context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	b.balances[senderModule] = b.balanceOf(senderModule).Sub(amount)
	b.balances[recipientAddr.String()] = b.balanceOf(recipientAddr.String()).Add(amount)
	return nil
}

// --- fake EarningsKeeper ---

type fakeEarningsKeeper struct {
	credited map[string]math.Int
	// failFor makes CreditEarnings fail for this address.
	failFor string
	// failWith, when set, is the error CreditEarnings returns for failFor.
	failWith error
}

func newFakeEarningsKeeper() *fakeEarningsKeeper {
	return &fakeEarningsKeeper{credited: make(map[string]math.Int)}
}

func (e *fakeEarningsKeeper) CreditEarnings(_ context.Context, _ string, addr sdk.AccAddress, amt sdk.Coin) error {
	if e.failFor != "" && e.failFor == addr.String() {
		if e.failWith != nil {
			return e.failWith
		}
		return fmt.Errorf("earnings of %s unavailable", addr)
	}
	key := addr.String()
	if v, ok := e.credited[key]; ok {
		e.credited[key] = v.Add(amt.Amount)
	} else {
		e.credited[key] = amt.Amount
	}
	return nil
}

// --- fake EmissionKeeper ---

type fakeEmissionKeeper struct{ epoch uint64 }

func (e *fakeEmissionKeeper) CurrentEpoch(_ context.Context) (uint64, error) { return e.epoch, nil }

// --- fixture ---

type testFixture struct {
	Ctx      sdk.Context
	Keeper   keeper.Keeper
	Staking  *fakeStakingKeeper
	Slashing *fakeSlashingKeeper
	Bank     *fakeBankKeeper
	Earnings *fakeEarningsKeeper
	Emission *fakeEmissionKeeper
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

	staking := newFakeStakingKeeper()
	slashing := newFakeSlashingKeeper()
	bank := newFakeBankKeeper()
	earnings := newFakeEarningsKeeper()
	emission := &fakeEmissionKeeper{epoch: 1}

	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), staking, slashing, bank, earnings)

	return &testFixture{Ctx: ctx, Keeper: k, Staking: staking, Slashing: slashing, Bank: bank, Earnings: earnings, Emission: emission}
}

func testPubKey(seed byte) []byte {
	pk := make([]byte, 32)
	for i := range pk {
		pk[i] = seed
	}
	return pk
}

func (f *testFixture) initGenesis(t *testing.T, mutate func(*types.GenesisState)) []byte {
	t.Helper()
	gs := types.DefaultGenesisState()
	if mutate != nil {
		mutate(gs)
	}
	_, err := f.Keeper.InitGenesis(f.Ctx, *gs, f.Emission)
	require.NoError(t, err)
	return nil
}
