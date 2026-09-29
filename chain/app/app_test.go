package app_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtprototypes "github.com/cometbft/cometbft/proto/tendermint/types"
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
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
	shieldedpolicy "github.com/DeBrosOfficial/network/chain/x/shielded/policy"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
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

// buildGenesisState returns a default genesis that starts at exactly zero norama supply, with a
// single-member x/power bootstrap committee giving that one seat all of the genesis CometBFT
// voting power (plans/open-network.md D16; plans/open-network/track-c-chain.md C4) - replacing the
// old devnet-only self-bonded-validator exception this test used before x/power existed (see
// docs/CHAIN.md).
func buildGenesisState(t *testing.T, oramaApp *app.OramaApp) (app.GenesisState, sdk.AccAddress) {
	t.Helper()

	genState := app.NewDefaultGenesisState(oramaApp)

	memberAddr := sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address())
	consPriv := cmted25519.GenPrivKey()
	consPubKeyBytes := consPriv.PubKey().Bytes()
	require.Len(t, consPubKeyBytes, powertypes.Ed25519PubKeyLen)

	powerGenState := powertypes.DefaultGenesisState()
	powerGenState.Params.MinCommitteeSize = 1 // testChainID contains "-localnet-": the devnet floor applies.
	powerGenState.BootstrapCommittee = []powertypes.BootstrapMember{{
		OperatorAddress: memberAddr.String(),
		Moniker:         "smoke-test-committee-member",
		ConsensusPubkey: consPubKeyBytes,
	}}
	genState[powertypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(powerGenState)

	// emission: a short epoch (2 seconds, 1 block minimum) so the smoke test can close an epoch in
	// a handful of FinalizeBlock calls instead of simulating a real genesis-scale epoch.
	// allow_bootstrap_stake only relaxes the epoch-duration/min-blocks floors here - genesis supply
	// is exactly zero either way, so its premine gate is satisfied trivially.
	emissionGenState := emissiontypes.DefaultGenesisState()
	emissionGenState.Params = emissiontypes.NewParams(2*time.Second, 1, true)
	genState[emissiontypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(emissionGenState)

	return genState, memberAddr
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

func TestUserToUserNoramaSendIsRefused(t *testing.T) {
	oramaApp := buildTestApp(t)
	ctx := oramaApp.NewUncachedContext(true, cmtprototypes.Header{})
	from := sdk.AccAddress(bytes.Repeat([]byte{1}, 20))
	to := sdk.AccAddress(bytes.Repeat([]byte{2}, 20))
	err := oramaApp.BankKeeper.SendCoins(ctx, from, to, sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)))
	require.ErrorIs(t, err, shieldedpolicy.ErrPublicPayment)

	err = oramaApp.BankKeeper.SendCoins(ctx, from, to, sdk.NewCoins(sdk.NewInt64Coin("ufoo", 1)))
	require.NotErrorIs(t, err, shieldedpolicy.ErrPublicPayment)

	module := authtypes.NewModuleAddress(emissiontypes.ModuleName)
	err = oramaApp.BankKeeper.SendCoins(ctx, module, to, sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)))
	require.NotErrorIs(t, err, shieldedpolicy.ErrPublicPayment)
}

// TestOramaApp_zeroSupplyGenesisProducesBlocksAndPaysEarnings drives InitChain, from an exactly
// zero-supply genesis with a single-member x/power bootstrap committee, plus enough
// FinalizeBlock+Commit cycles for one emission epoch to close, and checks that:
//   - InitChain accepted the bootstrap committee as the genesis CometBFT validator set (no gentx,
//     no self-bond - plans/open-network.md D16);
//   - the epoch-1 validator/delegator share was minted and landed in the committee member's
//     earnings account on its own capped power (plans/open-network/track-c-chain.md C3 "It does
//     not use the stock distribution module"; C4), not the fee collector/x/distribution;
//   - the supply invariant still holds.
func TestOramaApp_zeroSupplyGenesisProducesBlocksAndPaysEarnings(t *testing.T) {
	oramaApp := buildTestApp(t)

	genState, memberAddr := buildGenesisState(t, oramaApp)
	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)

	genesisTime := time.Unix(1_700_000_000, 0)
	initResp, err := oramaApp.InitChain(&abci.RequestInitChain{
		ChainId:       testChainID,
		InitialHeight: 1,
		Time:          genesisTime,
		AppStateBytes: stateBytes,
	})
	require.NoError(t, err)
	require.Len(t, initResp.Validators, 1, "the sole bootstrap committee member must be the genesis validator set")

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

	ctx := oramaApp.NewContext(true)
	epochState, err := oramaApp.EmissionKeeper.EpochState.Get(ctx)
	require.NoError(t, err)

	require.True(t, epochState.GenesisSupply.IsZero(), "a bootstrap-committee genesis needs no self-bond, so genesis supply is exactly zero")
	require.Equal(t, uint64(2), epochState.CurrentEpoch, "one epoch must have closed by now")
	require.True(t, epochState.CumulativeMinted.Equal(math.NewInt(epoch1ValidatorShareNorama)))

	// x/emission minted the share directly into x/power, which - as the chain's only validator,
	// at its full capped/bootstrap power - attributed the entire amount to the committee member
	// (its sole delegator is itself, so the whole share is "its own"; see
	// power/keeper.Keeper.distributeValidatorReward). Half of that own share is force-bonded into
	// its self-delegation by default (Params.ForceBondFraction), capped at
	// Params.SelfBondCapMultiplier * Params.MinSelfBond; the rest lands in its earnings account.
	// Nothing reaches the fee collector or x/distribution for this mint.
	feeCollectorBalance := oramaApp.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(authtypes.FeeCollectorName), params.BaseDenom)
	require.True(t, feeCollectorBalance.Amount.IsZero(), "the emission mint must never reach the fee collector")

	distrBalance := oramaApp.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(distrtypes.ModuleName), params.BaseDenom)
	require.True(t, distrBalance.Amount.IsZero(), "the emission mint must never reach x/distribution")

	// Default force-bond ceiling (SelfBondCapMultiplier=2 * MinSelfBond=1,000 ORAMA) binds before
	// half of the epoch-1 share would: the ceiling amount is force-bonded, and the rest reaches
	// earnings.
	wantForceBonded := powertypes.DefaultParams().SelfBondCapMultiplier.MulInt(powertypes.DefaultParams().MinSelfBond).TruncateInt()
	wantEarnings := math.NewInt(epoch1ValidatorShareNorama).Sub(wantForceBonded)

	memberEarnings, err := oramaApp.FeesKeeper.GetEarnings(ctx, memberAddr)
	require.NoError(t, err)
	require.True(t, memberEarnings.Equal(wantEarnings),
		"the epoch-1 validator share minus the force-bonded amount must have reached the committee member's earnings account: want %s, got %s", wantEarnings, memberEarnings)

	memberValoper := sdk.ValAddress(memberAddr).String()
	valAddr, err := sdk.ValAddressFromBech32(memberValoper)
	require.NoError(t, err)
	validator, err := oramaApp.StakingKeeper.GetValidator(ctx, valAddr)
	require.NoError(t, err)
	require.True(t, validator.Tokens.Equal(wantForceBonded),
		"the force-bonded amount must have been self-delegated into the committee member's own validator")

	supply := oramaApp.BankKeeper.GetSupply(ctx, params.BaseDenom)
	require.True(t, supply.Amount.Equal(epochState.GenesisSupply.Add(epochState.CumulativeMinted)))

	_, broken := oramaApp.EmissionKeeper.CheckSupplyInvariant(ctx)
	require.False(t, broken)
}

// TestBlockedAddresses_coversEveryModuleAccount confirms every module account address is blocked
// from receiving a direct bank send, matching every standard Cosmos SDK app.
func TestBlockedAddresses_coversEveryModuleAccount(t *testing.T) {
	blocked := app.BlockedAddresses()
	for name := range app.ModuleAccountPerms() {
		addr := authtypes.NewModuleAddress(name).String()
		require.True(t, blocked[addr], "module account %s must be blocked", name)
	}
}

// TestGetMaccPerms_onlyEmissionMintsNorama confirms x/emission is the only module that can mint
// norama - the whole point of not wiring x/mint (plans/open-network.md: "Only x/emission may
// mint"). x/token also holds Minter for the denoms it creates, but its bank keeper refuses norama.
func TestGetMaccPerms_onlyEmissionMintsNorama(t *testing.T) {
	var minters []string
	for name, perms := range app.GetMaccPerms() {
		for _, perm := range perms {
			if perm == authtypes.Minter {
				minters = append(minters, name)
			}
		}
	}
	require.ElementsMatch(t, []string{emissiontypes.ModuleName, tokentypes.ModuleName}, minters)
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

// The app wires both verifiers the spec requires, and a node that has neither the Rust library
// nor the out-of-process binary configured accepts no bundle.
func TestOramaApp_shieldedVerifiersAreTwoAndAnUnconfiguredNodeAcceptsNothing(t *testing.T) {
	a := buildTestApp(t) // no --shielded-verifier and no home
	require.Len(t, a.ShieldedVerifiers, verify.MinVerifiers)
	require.Error(t, verify.Check([]byte{1}, nil, a.ShieldedVerifiers...))

	// Whatever the first verifier says about these bytes, the second one, the out-of-process
	// binary, is not configured and must refuse on its own.
	require.ErrorIs(t, a.ShieldedVerifiers[1].Verify(shieldedTestBundle(), nil), verify.ErrVerifierNotLinked)
}

// An app with no chain id is the CLI's throwaway metadata instance, not a node. It builds no
// verifier bound to the empty chain id, and every bundle is refused.
func TestOramaApp_noChainIDBuildsNoShieldedVerifier(t *testing.T) {
	app.SetAddressPrefixes()
	a := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
	if len(a.ShieldedVerifiers) != 0 {
		t.Fatalf("expected no verifiers without a chain id, got %d", len(a.ShieldedVerifiers))
	}
	if err := verify.Check([]byte{1}, nil, a.ShieldedVerifiers...); err != verify.ErrVerifierNotLinked {
		t.Fatalf("got %v, want ErrVerifierNotLinked", err)
	}
}
