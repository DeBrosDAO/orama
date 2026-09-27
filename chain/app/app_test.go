package app_test

import (
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtprototypes "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	consensustypes "github.com/cosmos/cosmos-sdk/x/consensus/types"
	distrkeeper "github.com/cosmos/cosmos-sdk/x/distribution/keeper"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	upgradekeeper "github.com/cosmos/cosmos-sdk/x/upgrade/keeper"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// testChainID is the chain ID used across this file's InitChain calls; baseapp.SetChainID must
// be given the same value at app construction time or InitChain rejects every request. It must
// contain "-localnet-" so x/emission's bootstrap-stake premine gate accepts this genesis's
// nonzero supply (see buildGenesisState).
const testChainID = "orama-localnet-apptest-1"

// epoch1ValidatorShareNorama is epoch 1's validator/delegator share, hard-coded (see
// TestSplitEpochMint_epoch1TotalSplitsExactly in x/emission/types), not computed via
// emissiontypes.SplitEpochMint here.
const epoch1ValidatorShareNorama = 8_908_800_000_000

func buildTestApp(t *testing.T) *app.OramaApp {
	t.Helper()
	app.SetAddressPrefixes()
	return app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID(testChainID))
}

// buildGenesisState returns a default genesis with one bonded validator whose self-bond
// (sdk.DefaultPowerReduction norama) is the entire genesis supply - the devnet-only
// bootstrap-stake exception documented on emissionkeeper.Keeper.InitGenesis and
// plans/open-network.md D16 point 4. The genesis account itself is given zero free balance, so
// every norama of genesis supply sits in the staking bonded pool, satisfying x/emission's
// bootstrap-stake premine gate (genesis supply must equal the bonded pool balance exactly, with
// nothing left idle in a plain account).
func buildGenesisState(t *testing.T, oramaApp *app.OramaApp) app.GenesisState {
	t.Helper()

	genAddr := sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address())
	genAcc := authtypes.NewBaseAccount(genAddr, nil, 0, 0)
	zeroBalance := banktypes.Balance{
		Address: genAddr.String(),
		Coins:   sdk.NewCoins(),
	}

	// The validator set needs a CometBFT-native ed25519 key (distinct from the SDK-wrapped
	// cryptotypes.PrivKey used for accounts): cmttypes.NewValidator and
	// simtestutil.GenesisStateWithValSet both expect cometbft/crypto.PubKey.
	consPriv := cmted25519.GenPrivKey()
	cmtValidator := cmttypes.NewValidator(consPriv.PubKey(), 1)
	valSet := cmttypes.NewValidatorSet([]*cmttypes.Validator{cmtValidator})

	genState := app.NewDefaultGenesisState(oramaApp)
	genState, err := simtestutil.GenesisStateWithValSet(
		oramaApp.AppCodec(),
		genState,
		valSet,
		[]authtypes.GenesisAccount{genAcc},
		zeroBalance,
	)
	require.NoError(t, err)

	// emission: a short epoch (2 seconds, 1 block minimum) so the smoke test can close an epoch
	// in a handful of FinalizeBlock calls instead of simulating a real genesis-scale epoch.
	// allow_bootstrap_stake must be set for this non-zero-supply devnet-style genesis to pass
	// InitGenesis's premine gate.
	emissionGenState := emissiontypes.DefaultGenesisState()
	emissionGenState.Params = emissiontypes.NewParams(2*time.Second, 1, true)
	genState[emissiontypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(emissionGenState)

	return genState
}

// TestOramaApp_buildsAndValidatesDefaultGenesis confirms the app wires without panicking and
// that every registered module's default genesis (including x/emission's) marshals and validates
// cleanly through the app's own codec - the same path `oramad init` and `oramad genesis validate`
// exercise. The full InitChain path (with a funded, self-delegating validator) is exercised by
// TestOramaApp_epochBoundaryMintsToFeeCollector below.
func TestOramaApp_buildsAndValidatesDefaultGenesis(t *testing.T) {
	oramaApp := buildTestApp(t)

	genState := app.NewDefaultGenesisState(oramaApp)
	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)
	require.NotEmpty(t, stateBytes)

	require.NoError(t, emissiontypes.DefaultGenesisState().Validate())

	var emissionRaw json.RawMessage = genState[emissiontypes.ModuleName]
	require.NotEmpty(t, emissionRaw, "the default genesis must include an emission section")
	var emissionGenState emissiontypes.GenesisState
	require.NoError(t, oramaApp.AppCodec().UnmarshalJSON(emissionRaw, &emissionGenState))
	require.True(t, emissionGenState.EpochState.CumulativeMinted.IsZero(), "genesis supply must be zero")
	require.False(t, emissionGenState.Params.AllowBootstrapStake, "a normal default genesis must not allow bootstrap stake")

	var bankGenState banktypes.GenesisState
	require.NoError(t, oramaApp.AppCodec().UnmarshalJSON(genState[banktypes.ModuleName], &bankGenState))
	require.Len(t, bankGenState.DenomMetadata, 1)
	require.Equal(t, params.BaseDenom, bankGenState.DenomMetadata[0].Base)
	require.Equal(t, params.DisplayDenom, bankGenState.DenomMetadata[0].Display)

	var distrGenState distrtypes.GenesisState
	require.NoError(t, oramaApp.AppCodec().UnmarshalJSON(genState[distrtypes.ModuleName], &distrGenState))
	require.True(t, distrGenState.Params.CommunityTax.IsZero(), "community_tax must default to zero: there is no spend path for it")
}

// TestOramaApp_epochBoundaryMintsToFeeCollector drives InitChain plus enough FinalizeBlock+Commit
// cycles for one emission epoch to close, and checks that the validator/delegator share actually
// reached the bonded validator via x/distribution (plans/open-network/track-c-chain.md C3: "the
// validator share is minted and sent to the fee collector"; see docs/CHAIN.md's Deviations for why
// stock x/distribution, not a custom reward path, is what actually pays it out today).
func TestOramaApp_epochBoundaryMintsToFeeCollector(t *testing.T) {
	oramaApp := buildTestApp(t)

	genState := buildGenesisState(t, oramaApp)
	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)

	genesisTime := time.Unix(1_700_000_000, 0)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{
		ChainId:       testChainID,
		InitialHeight: 1,
		Time:          genesisTime,
		AppStateBytes: stateBytes,
	})
	require.NoError(t, err)

	// InitChain does not commit anything itself: per its own doc comment, "FinalizeBlock for
	// block InitialHeight starts from this FinalizeBlockState". The first real block to run is
	// FinalizeBlock(height=InitialHeight); only then does Commit() persist InitChain's writes
	// together with that block's. Calling Commit() directly after InitChain (with no
	// FinalizeBlock in between) would discard the genesis state entirely.
	//
	// A single block, 2 seconds after genesis, already satisfies both closing conditions
	// (epoch_duration=2s elapsed, min_blocks_per_epoch=1 block produced), so exactly one epoch
	// boundary is crossed by this one FinalizeBlock+Commit.
	blockTime := genesisTime.Add(2 * time.Second)
	_, err = oramaApp.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: 1,
		Time:   blockTime,
	})
	require.NoError(t, err)
	_, err = oramaApp.Commit()
	require.NoError(t, err)

	// x/distribution only allocates the fee collector's balance on height > 1 (it needs the
	// previous block's proposer/vote info first - see x/distribution/keeper/abci.go), so a second
	// block is needed before the epoch-1 mint actually leaves the fee collector. It closes 1s
	// later, well under the 2s epoch_duration, so no second epoch closes here.
	_, err = oramaApp.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: 2,
		Time:   blockTime.Add(time.Second),
	})
	require.NoError(t, err)
	_, err = oramaApp.Commit()
	require.NoError(t, err)

	ctx := oramaApp.NewContext(true)
	epochState, err := oramaApp.EmissionKeeper.EpochState.Get(ctx)
	require.NoError(t, err)

	// The devnet-only bootstrap supply (the validator's self-bond, funded from the genesis
	// account) is captured once, at genesis, as x/emission's genesis_supply. It equals
	// sdk.DefaultPowerReduction norama, since that is what simtestutil.GenesisStateWithValSet
	// bonds a validator with.
	require.True(t, epochState.GenesisSupply.Equal(sdk.DefaultPowerReduction))
	require.Equal(t, uint64(2), epochState.CurrentEpoch, "one epoch must have closed by now")
	require.True(t, epochState.CumulativeMinted.Equal(math.NewInt(epoch1ValidatorShareNorama)))

	// x/emission minted into the fee collector at height 1; x/distribution's own BeginBlocker
	// (which runs immediately after x/emission's, see app.go's SetOrderBeginBlockers) only
	// allocates the fee collector's balance starting at height 2 (it needs the previous block's
	// vote info first), which is why the smoke test above runs a second block. By height 2 the fee
	// collector is swept empty and the funds sit in the distribution module account.
	feeCollectorBalance := oramaApp.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(authtypes.FeeCollectorName), params.BaseDenom)
	require.True(t, feeCollectorBalance.Amount.IsZero(), "distribution must have swept the fee collector by the end of the block")

	distrBalance := oramaApp.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(distrtypes.ModuleName), params.BaseDenom)
	require.True(t, distrBalance.Amount.Equal(math.NewInt(epoch1ValidatorShareNorama)),
		"the validator share must have reached the distribution module account, got %s", distrBalance.Amount)

	supply := oramaApp.BankKeeper.GetSupply(ctx, params.BaseDenom)
	require.True(t, supply.Amount.Equal(epochState.GenesisSupply.Add(epochState.CumulativeMinted)))

	_, broken := oramaApp.EmissionKeeper.CheckSupplyInvariant(ctx)
	require.False(t, broken)
}

// TestBlockedAddresses_coversEveryModuleAccount confirms every module account address is blocked
// from receiving a direct bank send, matching every standard Cosmos SDK app.
func TestBlockedAddresses_coversEveryModuleAccount(t *testing.T) {
	blocked := app.BlockedAddresses()
	for name := range app.GetMaccPerms() {
		addr := authtypes.NewModuleAddress(name).String()
		require.True(t, blocked[addr], "module account %s must be blocked", name)
	}
}

// TestGetMaccPerms_exactlyOneMinter confirms x/emission is the only module account allowed to
// mint - the whole point of not wiring x/mint (plans/open-network.md: "Only x/emission may mint").
func TestGetMaccPerms_exactlyOneMinter(t *testing.T) {
	minters := 0
	var minterName string
	for name, perms := range app.GetMaccPerms() {
		for _, perm := range perms {
			if perm == authtypes.Minter {
				minters++
				minterName = name
			}
		}
	}
	require.Equal(t, 1, minters, "exactly one module account may hold the Minter permission")
	require.Equal(t, emissiontypes.ModuleName, minterName)
}

// TestUnreachableAuthority_rejectsEveryAuthorityGatedMsg confirms that bank, staking,
// distribution, consensus and upgrade's authority-gated messages all reject a signer that isn't
// app.UnreachableAuthority() - which every signer necessarily is, since no private key can ever
// produce that address's signature. This is what actually makes every "governance" knob on this
// chain unreachable (plans/open-network.md D18), not just the choice of address.
func TestUnreachableAuthority_rejectsEveryAuthorityGatedMsg(t *testing.T) {
	oramaApp := buildTestApp(t)
	ctx := oramaApp.NewContext(true)

	wrongAuthority := sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address()).String()
	require.NotEqual(t, app.UnreachableAuthority(), wrongAuthority)

	t.Run("bank MsgUpdateParams", func(t *testing.T) {
		msgServer := bankkeeper.NewMsgServerImpl(oramaApp.BankKeeper)
		_, err := msgServer.UpdateParams(ctx, &banktypes.MsgUpdateParams{
			Authority: wrongAuthority,
			Params:    banktypes.DefaultParams(),
		})
		require.Error(t, err)
	})

	t.Run("staking MsgUpdateParams", func(t *testing.T) {
		msgServer := stakingkeeper.NewMsgServerImpl(oramaApp.StakingKeeper)
		_, err := msgServer.UpdateParams(ctx, &stakingtypes.MsgUpdateParams{
			Authority: wrongAuthority,
			Params:    stakingtypes.DefaultParams(),
		})
		require.Error(t, err)
	})

	t.Run("distribution MsgUpdateParams", func(t *testing.T) {
		msgServer := distrkeeper.NewMsgServerImpl(oramaApp.DistrKeeper)
		_, err := msgServer.UpdateParams(ctx, &distrtypes.MsgUpdateParams{
			Authority: wrongAuthority,
			Params:    distrtypes.DefaultParams(),
		})
		require.Error(t, err)
	})

	t.Run("consensus MsgUpdateParams", func(t *testing.T) {
		_, err := oramaApp.ConsensusParamsKeeper.UpdateParams(ctx, &consensustypes.MsgUpdateParams{
			Authority: wrongAuthority,
			Block:     &cmtprototypes.BlockParams{MaxBytes: 1, MaxGas: 1},
		})
		require.Error(t, err)
	})

	t.Run("upgrade MsgSoftwareUpgrade", func(t *testing.T) {
		msgServer := upgradekeeper.NewMsgServerImpl(oramaApp.UpgradeKeeper)
		_, err := msgServer.SoftwareUpgrade(ctx, &upgradetypes.MsgSoftwareUpgrade{
			Authority: wrongAuthority,
			Plan:      upgradetypes.Plan{Name: "test-upgrade", Height: 1_000_000},
		})
		require.Error(t, err)
	})
}
