package app_test

import (
	"sort"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	housetypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// expectedModuleAccountPerms is the whole set of module accounts and what each may do with coins. A new
// module account, or a new permission on an old one, fails TestModuleAccountPermissions_exactSet until
// this table is changed in the same commit, which is when a reviewer looks at who can now create or
// destroy coins.
func expectedModuleAccountPerms() map[string][]string {
	perms := map[string][]string{
		authtypes.FeeCollectorName:     nil,
		distrtypes.ModuleName:          nil,
		stakingtypes.BondedPoolName:    {authtypes.Burner, authtypes.Staking},
		stakingtypes.NotBondedPoolName: {authtypes.Burner, authtypes.Staking},
		emissiontypes.ModuleName:       {authtypes.Minter},
		powertypes.ModuleName:          nil,
		feestypes.ModuleName:           {authtypes.Burner},
		feestypes.DepositsModuleName:   {authtypes.Burner},
		tokentypes.ModuleName:          {authtypes.Minter, authtypes.Burner},
		archivetypes.ModuleName:        nil,
		nodestypes.ModuleName:          {authtypes.Burner},
		housetypes.ModuleName:          {authtypes.Burner},
		storagetypes.ModuleName:        {authtypes.Burner},
		storagetypes.EscrowModuleName:  {authtypes.Burner},
		storagetypes.ArchiveModuleName: nil,
		relaytypes.ModuleName:          {authtypes.Burner},
		cnfttypes.ModuleName:           nil,
		markettypes.ModuleName:         nil,
		shieldedtypes.ModuleName:       {authtypes.Burner},
	}
	if app.WasmVMLinked() {
		perms["wasm"] = nil
	}
	return perms
}

func holdersOf(perms map[string][]string, perm string) []string {
	var out []string
	for name, ps := range perms {
		for _, p := range ps {
			if p == perm {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Norama is created only where a module holds Minter, and the chain's supply check (conservation.go)
// relies on that set. The set is read from the permission table the auth keeper really uses, not from
// the table in app.go, so a wiring change that bypasses the table fails here too.
func TestModuleAccountPermissions_exactSet(t *testing.T) {
	want := normalisePerms(expectedModuleAccountPerms())
	require.Equal(t, want, normalisePerms(app.ModuleAccountPerms()), "app.ModuleAccountPerms")

	oramaApp := buildTestApp(t)
	live := map[string][]string{}
	for name, p := range oramaApp.AccountKeeper.GetModulePermissions() {
		live[name] = p.GetPermissions()
	}
	require.Equal(t, want, normalisePerms(live), "the auth keeper's permission table")

	require.Equal(t, []string{emissiontypes.ModuleName, tokentypes.ModuleName}, holdersOf(live, authtypes.Minter), "the only modules that can mint")
	burners := holdersOf(want, authtypes.Burner)
	require.Equal(t, burners, holdersOf(live, authtypes.Burner), "the modules that can burn")
	require.NotContains(t, holdersOf(live, authtypes.Minter), powertypes.ModuleName)
}

func normalisePerms(in map[string][]string) map[string][]string {
	out := map[string][]string{}
	for name, ps := range in {
		cp := append([]string(nil), ps...)
		sort.Strings(cp)
		if len(cp) == 0 {
			cp = nil
		}
		out[name] = cp
	}
	return out
}

// conservationChain is a one-member chain at height 2, where the next FinalizeBlock is the first that
// can run the end-of-block check over a state a test has damaged.
type conservationChain struct {
	t       *testing.T
	app     *app.OramaApp
	genesis time.Time
	height  int64
}

func newConservationChain(t *testing.T, minBlocksPerEpoch uint64) *conservationChain {
	t.Helper()
	oramaApp := buildTestApp(t)
	genState, _ := committeeGenesis(t, oramaApp, 1, 365)
	emission := emissiontypes.DefaultGenesisState()
	emission.Params = emissiontypes.NewParams(2*time.Second, minBlocksPerEpoch, true)
	genState[emissiontypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(emission)
	c := &conservationChain{t: t, app: oramaApp, genesis: time.Unix(1_700_000_000, 0), height: 1}
	initChain(t, oramaApp, genState, simMaxGas, c.genesis)
	finalize(t, oramaApp, 1, c.at(1))
	return c
}

func (c *conservationChain) at(h int64) time.Time {
	return c.genesis.Add(time.Duration(h) * 2 * time.Second)
}

// damage applies fn to the state as a committed block that ran no end-of-block check, the way a bug in a
// handler would leave it.
func (c *conservationChain) damage(fn func(ctx sdk.Context)) {
	c.t.Helper()
	c.height++
	ctx := c.app.NewNextBlockContext(cmtproto.Header{Height: c.height, Time: c.at(c.height), ChainID: testChainID})
	fn(ctx)
	writeCache(c.t, ctx)
	_, err := c.app.Commit()
	require.NoError(c.t, err)
}

func (c *conservationChain) nextBlock() error {
	c.height++
	if _, err := c.app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: c.height, Time: c.at(c.height)}); err != nil {
		return err
	}
	_, err := c.app.Commit()
	return err
}

func oneNorama() sdk.Coins { return sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)) }

// A mint that does not tell x/emission is the failure the supply check exists for: no module but
// x/emission may create norama, and x/emission books everything it creates.
func TestConservation_haltsOnNoramaCreatedOutsideEmission(t *testing.T) {
	c := newConservationChain(t, 1)
	c.damage(func(ctx sdk.Context) {
		require.NoError(t, c.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, oneNorama()))
	})
	err := c.nextBlock()
	require.ErrorContains(t, err, "conservation violated")
	require.ErrorContains(t, err, "was created outside x/emission")
}

// A coin in a denom nobody created is a mint that bypassed x/token.
func TestConservation_haltsOnADenomWithNoTokenRecord(t *testing.T) {
	c := newConservationChain(t, 1)
	c.damage(func(ctx sdk.Context) {
		rogue := sdk.NewCoins(sdk.NewInt64Coin("factory/orama1rogue/gold", 5))
		require.NoError(t, c.app.BankKeeper.MintCoins(ctx, tokentypes.ModuleName, rogue))
	})
	err := c.nextBlock()
	require.ErrorContains(t, err, "conservation violated")
	require.ErrorContains(t, err, "x/token has no record of it")
}

func TestConservation_haltsOnADenomThatIsNotNoramaOrFactory(t *testing.T) {
	c := newConservationChain(t, 1)
	c.damage(func(ctx sdk.Context) {
		require.NoError(t, c.app.BankKeeper.MintCoins(ctx, tokentypes.ModuleName, sdk.NewCoins(sdk.NewInt64Coin("ibcfoo", 5))))
	})
	require.ErrorContains(t, c.nextBlock(), "is not norama or a factory denom")
}

// A ledger that claims coins its module account does not hold, here the earnings ledger.
func TestConservation_haltsWhenALedgerClaimsMoreThanItsModuleHolds(t *testing.T) {
	c := newConservationChain(t, 1)
	c.damage(func(ctx sdk.Context) {
		row := sdk.AccAddress([]byte("conservation_test_ow")).String()
		require.NoError(t, c.app.FeesKeeper.Earnings.Set(ctx, row, math.NewInt(1_000_000)))
	})
	err := c.nextBlock()
	require.ErrorContains(t, err, "x/fees")
	require.ErrorContains(t, err, "earnings match module")
}

// Coins in a staking pool that no validator or unbonding entry accounts for.
func TestConservation_haltsWhenAStakingPoolHoldsCoinsNoOneOwns(t *testing.T) {
	c := newConservationChain(t, 1)
	c.damage(func(ctx sdk.Context) {
		coins := oneNorama()
		require.NoError(t, mintNoramaForTest(c.app, ctx, coins))
		require.NoError(t, c.app.BankKeeper.SendCoinsFromModuleToModule(ctx, emissiontypes.ModuleName, stakingtypes.BondedPoolName, coins))
	})
	err := c.nextBlock()
	require.ErrorContains(t, err, "staking pools")
	require.ErrorContains(t, err, "bonded pool holds")
}

// A rise in the supply that is booked with x/emission is not a violation: only the ledger and the
// supply must agree. (Burns are reconciled in the same block; every block of the simulation with a
// transaction burns its fee.)
func TestConservation_acceptsMintsBookedWithEmission(t *testing.T) {
	c := newConservationChain(t, 1)
	c.damage(func(ctx sdk.Context) { require.NoError(t, mintNoramaForTest(c.app, ctx, oneNorama())) })
	require.NoError(t, c.nextBlock())
	require.NoError(t, c.nextBlock())
}

// The full pass runs every min_blocks_per_epoch blocks; the supply check runs every block.
func TestConservation_fullPassRunsOncePerMinBlocksPerEpoch(t *testing.T) {
	c := newConservationChain(t, 5)
	c.damage(func(ctx sdk.Context) { // height 2
		require.NoError(t, c.app.FeesKeeper.Earnings.Set(ctx, sdk.AccAddress([]byte("conservation_test_ow")).String(), math.NewInt(1)))
	})
	require.NoError(t, c.nextBlock(), "height 3 is not a multiple of 5")
	require.NoError(t, c.nextBlock(), "height 4 is not a multiple of 5")
	err := c.nextBlock() // height 5
	require.ErrorContains(t, err, "earnings match module")
}

func TestConservation_exportRefusesAStateThatBreaksConservation(t *testing.T) {
	c := newConservationChain(t, 1)
	c.damage(func(ctx sdk.Context) {
		require.NoError(t, c.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, oneNorama()))
	})
	_, err := c.app.ExportAppStateAndValidators(false, nil, nil)
	require.ErrorContains(t, err, "refusing to export a state that breaks conservation")
}

func TestConservation_cleanChainExportsAndPassesTheFullPass(t *testing.T) {
	c := newConservationChain(t, 1)
	require.NoError(t, c.nextBlock())
	require.NoError(t, c.app.CheckConservationFull(c.app.NewContext(true)))
	_, err := c.app.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
}
