package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission/keeper"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

const (
	testFaucetMaxDrip   int64 = 1_000
	testFaucetEpochCap  int64 = 2_500
	testFaucetCooldown        = 100 * time.Second
	testProductionChain       = "orama-1"
)

var (
	testFaucetSigner    = sdk.AccAddress("faucet_signer_address")
	testFaucetRecipient = sdk.AccAddress("faucet_recipient_addr")
	testOtherRecipient  = sdk.AccAddress("faucet_other_recipient")
)

// faucetFixture is a fixture whose genesis enables the faucet with small, round limits.
func faucetFixture(t *testing.T) *testFixture {
	t.Helper()
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
		gs.Params.FaucetEnabled = true
		gs.Params.FaucetMaxDrip = math.NewInt(testFaucetMaxDrip)
		gs.Params.FaucetEpochCap = math.NewInt(testFaucetEpochCap)
		gs.Params.FaucetRecipientCooldownSeconds = uint64(testFaucetCooldown / time.Second)
	})
	return f
}

func (f *testFixture) faucet(to sdk.AccAddress, amount math.Int) (*types.MsgFaucetResponse, error) {
	return keeper.NewMsgServer(f.Keeper).Faucet(f.Ctx, &types.MsgFaucet{
		Signer:    testFaucetSigner.String(),
		Recipient: to.String(),
		Amount:    amount,
	})
}

func (f *testFixture) faucetMinted(t *testing.T) math.Int {
	t.Helper()
	state, err := f.Keeper.EpochState.Get(f.Ctx)
	require.NoError(t, err)
	return state.CumulativeFaucetMinted
}

func TestFaucet_happyPathPaysRecipientAndKeepsSupplyInvariant(t *testing.T) {
	f := faucetFixture(t)

	resp, err := f.faucet(testFaucetRecipient, math.NewInt(testFaucetMaxDrip))
	require.NoError(t, err)
	require.True(t, resp.Amount.Equal(math.NewInt(testFaucetMaxDrip)))

	require.True(t, f.Bank.GetBalance(f.Ctx, testFaucetRecipient, params.BaseDenom).Amount.Equal(math.NewInt(testFaucetMaxDrip)))
	require.True(t, f.faucetMinted(t).Equal(math.NewInt(testFaucetMaxDrip)))
	detail, broken := f.Keeper.CheckSupplyInvariant(f.Ctx)
	require.False(t, broken, detail)

	var found bool
	for _, ev := range f.Ctx.EventManager().Events() {
		if ev.Type != types.EventTypeFaucet {
			continue
		}
		found = true
		attrs := map[string]string{}
		for _, a := range ev.Attributes {
			attrs[a.Key] = a.Value
		}
		require.Equal(t, testFaucetRecipient.String(), attrs[types.AttributeKeyRecipient])
		require.Equal(t, testFaucetSigner.String(), attrs[types.AttributeKeySigner])
		require.Equal(t, "1000"+params.BaseDenom, attrs[types.AttributeKeyAmount])
	}
	require.True(t, found, "a faucet event must be emitted")
}

func TestFaucet_endBlockReconcileDoesNotMisattributeFaucetMint(t *testing.T) {
	f := faucetFixture(t)
	_, err := f.faucet(testFaucetRecipient, math.NewInt(testFaucetMaxDrip))
	require.NoError(t, err)

	require.NoError(t, f.Keeper.ReconcileBurns(f.Ctx))

	state, err := f.Keeper.EpochState.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, state.CumulativeBurned.IsZero())
}

func TestFaucet_refusedOnProductionChainID(t *testing.T) {
	f := faucetFixture(t)
	f.Ctx = f.Ctx.WithChainID(testProductionChain)

	_, err := f.faucet(testFaucetRecipient, math.NewInt(1))
	require.ErrorIs(t, err, types.ErrFaucetProduction)
	require.True(t, f.faucetMinted(t).IsZero())
}

func TestFaucet_refusedWhenDisabled(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)

	_, err := f.faucet(testFaucetRecipient, math.NewInt(1))
	require.ErrorIs(t, err, types.ErrFaucetDisabled)
}

func TestFaucet_refusedForBadAmounts(t *testing.T) {
	f := faucetFixture(t)
	for name, amount := range map[string]math.Int{
		"over max drip": math.NewInt(testFaucetMaxDrip + 1),
		"zero":          math.ZeroInt(),
		"negative":      math.NewInt(-5),
		"nil":           {},
	} {
		_, err := f.faucet(testFaucetRecipient, amount)
		require.ErrorIs(t, err, types.ErrFaucetAmount, name)
	}
	require.True(t, f.faucetMinted(t).IsZero())
}

func TestFaucet_refusedForModuleAndMalformedRecipients(t *testing.T) {
	f := faucetFixture(t)
	moduleAddr := sdk.AccAddress("a_module_account_addr")
	f.Bank.BlockAddr(moduleAddr)

	_, err := f.faucet(moduleAddr, math.NewInt(1))
	require.ErrorIs(t, err, types.ErrFaucetRecipient)

	_, err = keeper.NewMsgServer(f.Keeper).Faucet(f.Ctx, &types.MsgFaucet{
		Signer: testFaucetSigner.String(), Recipient: "not-an-address", Amount: math.NewInt(1),
	})
	require.ErrorIs(t, err, types.ErrFaucetRecipient)
	require.True(t, f.faucetMinted(t).IsZero())
}

func TestFaucet_cooldownRefusesThenAllowsAfterwards(t *testing.T) {
	f := faucetFixture(t)
	_, err := f.faucet(testFaucetRecipient, math.NewInt(10))
	require.NoError(t, err)

	f.Ctx = f.Ctx.WithBlockTime(f.Ctx.BlockTime().Add(testFaucetCooldown - time.Second))
	_, err = f.faucet(testFaucetRecipient, math.NewInt(10))
	require.ErrorIs(t, err, types.ErrFaucetCooldown)

	// Another recipient is unaffected.
	_, err = f.faucet(testOtherRecipient, math.NewInt(10))
	require.NoError(t, err)

	f.Ctx = f.Ctx.WithBlockTime(f.Ctx.BlockTime().Add(time.Second))
	_, err = f.faucet(testFaucetRecipient, math.NewInt(10))
	require.NoError(t, err)
	require.True(t, f.faucetMinted(t).Equal(math.NewInt(30)))
}

func TestFaucet_zeroCooldownAllowsBackToBackDrips(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
		gs.Params.FaucetEnabled = true
		gs.Params.FaucetRecipientCooldownSeconds = 0
	})
	for i := 0; i < 2; i++ {
		_, err := f.faucet(testFaucetRecipient, math.NewInt(1))
		require.NoError(t, err)
	}
}

func TestFaucet_epochCapRefusesThenResetsWhenEpochCloses(t *testing.T) {
	f := faucetFixture(t)
	for _, to := range []sdk.AccAddress{testFaucetRecipient, testOtherRecipient} {
		_, err := f.faucet(to, math.NewInt(testFaucetMaxDrip))
		require.NoError(t, err)
	}
	// 2,000 minted of a 2,500 cap: 501 does not fit, 500 does.
	third := sdk.AccAddress("faucet_third_recipient")
	_, err := f.faucet(third, math.NewInt(501))
	require.ErrorIs(t, err, types.ErrFaucetEpochCap)
	_, err = f.faucet(third, math.NewInt(500))
	require.NoError(t, err)

	state, err := f.Keeper.EpochState.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, state.FaucetEpochMinted.Equal(math.NewInt(testFaucetEpochCap)))

	// Closing the epoch resets the per-epoch counter but not the all-time total.
	closeEpochs(t, f, 1)
	state, err = f.Keeper.EpochState.Get(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.CurrentEpoch)
	require.True(t, state.FaucetEpochMinted.IsZero())
	require.True(t, state.CumulativeFaucetMinted.Equal(math.NewInt(testFaucetEpochCap)))

	_, err = f.faucet(sdk.AccAddress("faucet_fourth_recipient"), math.NewInt(testFaucetMaxDrip))
	require.NoError(t, err)
	detail, broken := f.Keeper.CheckSupplyInvariant(f.Ctx)
	require.False(t, broken, detail)
}

func TestInitGenesis_rejectsFaucetEnabledOnProductionChainID(t *testing.T) {
	f := newTestFixture(t)
	gs := types.DefaultGenesisState()
	gs.Params.FaucetEnabled = true

	err := f.Keeper.InitGenesis(f.Ctx.WithChainID(testProductionChain), *gs)
	require.ErrorContains(t, err, "faucet_enabled requires a chain-id")

	for _, chainID := range []string{"orama-devnet-1", "orama-stagenet-1", "orama-localnet-1"} {
		g := newTestFixture(t)
		require.NoError(t, g.Keeper.InitGenesis(g.Ctx.WithChainID(chainID), *gs), chainID)
	}
}

func TestInitGenesis_rejectsInvalidFaucetParams(t *testing.T) {
	for name, mutate := range map[string]func(*types.Params){
		"enabled with zero max drip":   func(p *types.Params) { p.FaucetEnabled, p.FaucetMaxDrip = true, math.ZeroInt() },
		"epoch cap below max drip":     func(p *types.Params) { p.FaucetEnabled, p.FaucetEpochCap = true, p.FaucetMaxDrip.SubRaw(1) },
		"negative max drip (disabled)": func(p *types.Params) { p.FaucetMaxDrip = math.NewInt(-1) },
	} {
		f := newTestFixture(t)
		gs := types.DefaultGenesisState()
		mutate(&gs.Params)
		require.Error(t, f.Keeper.InitGenesis(f.Ctx, *gs), name)
	}
}

func TestExportGenesis_roundTripsFaucetCounters(t *testing.T) {
	f := faucetFixture(t)
	_, err := f.faucet(testFaucetRecipient, math.NewInt(testFaucetMaxDrip))
	require.NoError(t, err)

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.True(t, exported.EpochState.CumulativeFaucetMinted.Equal(math.NewInt(testFaucetMaxDrip)))
	require.True(t, exported.EpochState.FaucetEpochMinted.Equal(math.NewInt(testFaucetMaxDrip)))
	require.NoError(t, exported.Validate())

	// Re-import into a chain whose bank already holds the exported supply.
	g := newTestFixture(t)
	g.Bank.SetGenesisSupply(f.Bank.supply)
	require.NoError(t, g.Keeper.InitGenesis(g.Ctx, *exported))
	again, err := g.Keeper.ExportGenesis(g.Ctx)
	require.NoError(t, err)
	require.Equal(t, exported.EpochState, again.EpochState)
	detail, broken := g.Keeper.CheckSupplyInvariant(g.Ctx)
	require.False(t, broken, detail)
}

func TestGenesisValidate_rejectsBrokenFaucetCounters(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.EpochState.FaucetEpochMinted = math.NewInt(5)
	require.ErrorContains(t, gs.Validate(), "exceeds cumulative_faucet_minted")

	gs = types.DefaultGenesisState()
	gs.EpochState.CumulativeFaucetMinted = math.NewInt(-1)
	require.ErrorContains(t, gs.Validate(), "cumulative_faucet_minted")
}
