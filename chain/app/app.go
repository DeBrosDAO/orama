// Package app wires oramad, the Orama L1's Cosmos SDK application. It is built from the SDK
// v0.54.4 simapp reference (github.com/cosmos/cosmos-sdk/tree/v0.54.4/simapp), trimmed to the
// module list decided in plans/open-network/track-c-chain.md (C1) and with x/emission added.
//
// Wired: auth, bank (norama user-to-user sends refused; see shielded policy), staking,
// slashing, distribution, consensus params, upgrade, genutil,
// evidence, feegrant, and x/emission.
//
// Deliberately not wired (plans/open-network/track-c-chain.md C1 "Not wired", and this task's
// spec): x/mint (replaced by x/emission), x/gov (no governance module exists yet; see the
// "authority" discussion below), x/circuit, x/crisis, x/nft, x/group, x/authz, x/epochs (its only
// use in upstream simapp is periodic hooks that x/emission implements directly in its own
// BeginBlocker), IBC and anything EVM/CosmWasm.
package app

import (
	"encoding/json"
	"fmt"
	"maps"

	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/gogoproto/proto"
	"github.com/spf13/cast"

	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"
	reflectionv1 "cosmossdk.io/api/cosmos/reflection/v1"
	"cosmossdk.io/client/v2/autocli"
	clienthelpers "cosmossdk.io/client/v2/helpers"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	nodeservice "github.com/cosmos/cosmos-sdk/client/grpc/node"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	runtimeservices "github.com/cosmos/cosmos-sdk/runtime/services"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/cosmos/cosmos-sdk/server/api"
	"github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	"github.com/cosmos/cosmos-sdk/std"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	sigtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/cosmos-sdk/version"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"
	authcodec "github.com/cosmos/cosmos-sdk/x/auth/codec"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	"github.com/cosmos/cosmos-sdk/x/auth/posthandler"
	authsims "github.com/cosmos/cosmos-sdk/x/auth/simulation"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	txmodule "github.com/cosmos/cosmos-sdk/x/auth/tx/config"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/consensus"
	consensusparamkeeper "github.com/cosmos/cosmos-sdk/x/consensus/keeper"
	consensusparamtypes "github.com/cosmos/cosmos-sdk/x/consensus/types"
	distr "github.com/cosmos/cosmos-sdk/x/distribution"
	distrkeeper "github.com/cosmos/cosmos-sdk/x/distribution/keeper"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	"github.com/cosmos/cosmos-sdk/x/evidence"
	evidencekeeper "github.com/cosmos/cosmos-sdk/x/evidence/keeper"
	evidencetypes "github.com/cosmos/cosmos-sdk/x/evidence/types"
	"github.com/cosmos/cosmos-sdk/x/feegrant"
	feegrantkeeper "github.com/cosmos/cosmos-sdk/x/feegrant/keeper"
	feegrantmodule "github.com/cosmos/cosmos-sdk/x/feegrant/module"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/cosmos/cosmos-sdk/x/slashing"
	slashingkeeper "github.com/cosmos/cosmos-sdk/x/slashing/keeper"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	"github.com/cosmos/cosmos-sdk/x/staking"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/cosmos-sdk/x/tx/signing"
	"github.com/cosmos/cosmos-sdk/x/upgrade"
	upgradekeeper "github.com/cosmos/cosmos-sdk/x/upgrade/keeper"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission"
	emissionkeeper "github.com/DeBrosOfficial/network/chain/x/emission/keeper"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	"github.com/DeBrosOfficial/network/chain/x/fees"
	feesante "github.com/DeBrosOfficial/network/chain/x/fees/ante"
	feeskeeper "github.com/DeBrosOfficial/network/chain/x/fees/keeper"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	"github.com/DeBrosOfficial/network/chain/x/power"
	powerante "github.com/DeBrosOfficial/network/chain/x/power/ante"
	powerkeeper "github.com/DeBrosOfficial/network/chain/x/power/keeper"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
	shieldedpolicy "github.com/DeBrosOfficial/network/chain/x/shielded/policy"
)

const appName = "oramad"

// unreachableAuthorityName hashes to UnreachableAuthority's address. It is deliberately not
// "gov" (or any other real module name): using "gov" would mean that simply registering a
// standard x/gov module in some future release silently hands it control of every
// authority-gated message on the chain today, with no explicit migration step. A dedicated,
// never-to-be-registered name forces that migration to be a visible, deliberate code change.
const unreachableAuthorityName = "orama/no-authority"

// UnreachableAuthority is the address used as the "authority" for every module that the upstream
// SDK expects to be governed by x/gov (upgrade, consensus params, bank, staking, slashing,
// distribution, ...). x/houses (plans/open-network.md D17) does not exist yet, and per D18 ("no
// admin keys, no multisig, no kill switch") this app deliberately wires no governance module at
// all: the address is the hash of unreachableAuthorityName, and because no module by that name is
// ever registered here, no private key or module account can ever produce a valid signature for
// it. Every authority-gated message on this chain is therefore permanently unreachable until a
// future release wires real governance and migrates this authority - consistent with D17/D20: the
// only way to change chain behavior today is a coordinated hard fork, not an on-chain vote.
//
// This is a function, not a package-level value: it must be called after SetAddressPrefixes has
// set the process-wide bech32 config, and NewOramaApp always runs after that call (see
// cmd/oramad/cmd/root.go). A package-level var would instead capture the SDK's default "cosmos"
// prefix at Go's static init time, before SetAddressPrefixes ever runs.
func UnreachableAuthority() string {
	return authtypes.NewModuleAddress(unreachableAuthorityName).String()
}

var (
	// DefaultNodeHome is the default home directory for oramad.
	DefaultNodeHome string

	// maccPerms lists every module account and the mint/burn permissions it holds. Only
	// x/emission may mint (and only the validator/delegator share, per
	// plans/open-network/track-c-chain.md C3); the staking pools burn as part of the standard
	// bond/unbond accounting, and x/fees' two module accounts burn the base fee and the 1%
	// deposit-burn share (C2). x/power holds no permissions at all: it only moves already-minted
	// coins between other modules' accounts and delegates on a committee member's behalf, through
	// x/staking's own keeper.
	maccPerms = map[string][]string{
		authtypes.FeeCollectorName:     nil,
		distrtypes.ModuleName:          nil,
		stakingtypes.BondedPoolName:    {authtypes.Burner, authtypes.Staking},
		stakingtypes.NotBondedPoolName: {authtypes.Burner, authtypes.Staking},
		emissiontypes.ModuleName:       {authtypes.Minter},
		powertypes.ModuleName:          nil,
		feestypes.ModuleName:           {authtypes.Burner},
		feestypes.DepositsModuleName:   {authtypes.Burner},
	}
)

var (
	_ runtime.AppI            = (*OramaApp)(nil)
	_ servertypes.Application = (*OramaApp)(nil)
)

// OramaApp is the Orama L1 application.
type OramaApp struct {
	*baseapp.BaseApp

	legacyAmino       *codec.LegacyAmino
	appCodec          codec.Codec
	txConfig          client.TxConfig
	interfaceRegistry types.InterfaceRegistry

	keys map[string]*storetypes.KVStoreKey

	AccountKeeper         authkeeper.AccountKeeper
	BankKeeper            bankkeeper.BaseKeeper
	StakingKeeper         *stakingkeeper.Keeper
	SlashingKeeper        slashingkeeper.Keeper
	DistrKeeper           distrkeeper.Keeper
	UpgradeKeeper         *upgradekeeper.Keeper
	EvidenceKeeper        evidencekeeper.Keeper
	ConsensusParamsKeeper consensusparamkeeper.Keeper
	FeeGrantKeeper        feegrantkeeper.Keeper
	EmissionKeeper        emissionkeeper.Keeper
	PowerKeeper           powerkeeper.Keeper
	FeesKeeper            feeskeeper.Keeper

	ModuleManager      *module.Manager
	BasicModuleManager module.BasicManager

	configurator module.Configurator
}

func init() {
	var err error
	DefaultNodeHome, err = clienthelpers.GetNodeHomeDirectory(".oramad")
	if err != nil {
		panic(err)
	}
}

// NewOramaApp returns a reference to a new, initialized OramaApp.
func NewOramaApp(
	logger log.Logger,
	db dbm.DB,
	loadLatest bool,
	appOpts servertypes.AppOptions,
	baseAppOptions ...func(*baseapp.BaseApp),
) *OramaApp {
	interfaceRegistry, _ := types.NewInterfaceRegistryWithOptions(types.InterfaceRegistryOptions{
		ProtoFiles: proto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec: address.Bech32Codec{
				Bech32Prefix: sdk.GetConfig().GetBech32AccountAddrPrefix(),
			},
			ValidatorAddressCodec: address.Bech32Codec{
				Bech32Prefix: sdk.GetConfig().GetBech32ValidatorAddrPrefix(),
			},
		},
	})
	appCodec := codec.NewProtoCodec(interfaceRegistry)
	legacyAmino := codec.NewLegacyAmino()
	txConfig := authtx.NewTxConfig(appCodec, authtx.DefaultSignModes)

	if err := interfaceRegistry.SigningContext().Validate(); err != nil {
		panic(err)
	}

	std.RegisterLegacyAminoCodec(legacyAmino)
	std.RegisterInterfaces(interfaceRegistry)

	bApp := baseapp.NewBaseApp(appName, logger, db, txConfig.TxDecoder(), baseAppOptions...)
	bApp.SetVersion(version.Version)
	bApp.SetInterfaceRegistry(interfaceRegistry)
	bApp.SetTxEncoder(txConfig.TxEncoder())
	// Security review B4 ("the base fee never rises"): v0.54's NewBaseApp disables the block gas
	// meter by default, so ctx.BlockGasMeter().GasConsumed() (what x/fees' AdvanceBaseFee reacts to)
	// would always read 0 regardless of how full a block actually was.
	bApp.SetDisableBlockGasMeter(false)

	keys := storetypes.NewKVStoreKeys(
		authtypes.StoreKey,
		banktypes.StoreKey,
		stakingtypes.StoreKey,
		distrtypes.StoreKey,
		slashingtypes.StoreKey,
		consensusparamtypes.StoreKey,
		upgradetypes.StoreKey,
		feegrant.StoreKey,
		evidencetypes.StoreKey,
		emissiontypes.StoreKey,
		powertypes.StoreKey,
		feestypes.StoreKey,
	)

	app := &OramaApp{
		BaseApp:           bApp,
		legacyAmino:       legacyAmino,
		appCodec:          appCodec,
		txConfig:          txConfig,
		interfaceRegistry: interfaceRegistry,
		keys:              keys,
	}

	app.ConsensusParamsKeeper = consensusparamkeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[consensusparamtypes.StoreKey]),
		UnreachableAuthority(),
		runtime.EventService{},
	)
	bApp.SetParamStore(app.ConsensusParamsKeeper.ParamsStore)

	app.AccountKeeper = authkeeper.NewAccountKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[authtypes.StoreKey]),
		authtypes.ProtoBaseAccount,
		maccPerms,
		authcodec.NewBech32Codec(params.Bech32Prefix),
		params.Bech32Prefix,
		UnreachableAuthority(),
	)

	app.BankKeeper = bankkeeper.NewBaseKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[banktypes.StoreKey]),
		app.AccountKeeper,
		BlockedAddresses(),
		UnreachableAuthority(),
		logger,
	)
	// A user cannot bank-send norama to another user. Module accounts still can.
	// Shielded bundles are a separate path and are not accepted until a verifier is linked.
	app.BankKeeper.AppendSendRestriction(shieldedpolicy.NoramaSendRestriction(BlockedAddresses()))

	enabledSignModes := append(authtx.DefaultSignModes, sigtypes.SignMode_SIGN_MODE_TEXTUAL)
	txConfigOpts := authtx.ConfigOptions{
		EnabledSignModes:           enabledSignModes,
		TextualCoinMetadataQueryFn: txmodule.NewBankKeeperCoinMetadataQueryFn(app.BankKeeper),
	}
	txConfig, err := authtx.NewTxConfigWithOptions(appCodec, txConfigOpts)
	if err != nil {
		panic(err)
	}
	app.txConfig = txConfig

	app.StakingKeeper = stakingkeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[stakingtypes.StoreKey]),
		app.AccountKeeper,
		app.BankKeeper,
		UnreachableAuthority(),
		authcodec.NewBech32Codec(params.Bech32PrefixValAddr),
		authcodec.NewBech32Codec(params.Bech32PrefixConsAddr),
	)

	app.DistrKeeper = distrkeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[distrtypes.StoreKey]),
		app.AccountKeeper,
		app.BankKeeper,
		app.StakingKeeper,
		authtypes.FeeCollectorName,
		UnreachableAuthority(),
	)

	// slashBaseFix wraps app.StakingKeeper so a Slash/SlashWithInfractionReason call - reached both
	// directly from x/slashing's downtime handling and indirectly from x/evidence's double-sign
	// handling (which routes through x/slashing's own keeper - see x/evidence/types.SlashingKeeper) -
	// computes its burn amount from the validator's REAL bonded tokens rather than
	// TokensFromConsensusPower(power) on the power CometBFT actually reports (security review B3):
	// x/power's own CometBFT power is on a completely different, non-token-proportional scale (it is
	// capped, redistributed, ramped and bootstrap-blended - see docs/CHAIN.md), so feeding it through
	// the stock conversion would slash the wrong amount entirely.
	app.SlashingKeeper = slashingkeeper.NewKeeper(
		appCodec,
		legacyAmino,
		runtime.NewKVStoreService(keys[slashingtypes.StoreKey]),
		newSlashBaseFix(app.StakingKeeper),
		UnreachableAuthority(),
	)

	app.FeeGrantKeeper = feegrantkeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[feegrant.StoreKey]),
		app.AccountKeeper,
	)

	app.StakingKeeper.SetHooks(
		stakingtypes.NewMultiStakingHooks(
			app.DistrKeeper.Hooks(),
			app.SlashingKeeper.Hooks(),
		),
	)

	skipUpgradeHeights := map[int64]bool{}
	for _, h := range cast.ToIntSlice(appOpts.Get(server.FlagUnsafeSkipUpgrades)) {
		skipUpgradeHeights[int64(h)] = true
	}
	homePath := cast.ToString(appOpts.Get(flags.FlagHome))
	app.UpgradeKeeper = upgradekeeper.NewKeeper(
		skipUpgradeHeights,
		runtime.NewKVStoreService(keys[upgradetypes.StoreKey]),
		appCodec,
		homePath,
		app.BaseApp,
		UnreachableAuthority(),
	)

	evidenceKeeper := evidencekeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[evidencetypes.StoreKey]),
		app.StakingKeeper,
		app.SlashingKeeper,
		app.AccountKeeper.AddressCodec(),
		runtime.ProvideCometInfoService(),
	)
	app.EvidenceKeeper = *evidenceKeeper

	app.FeesKeeper = feeskeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[feestypes.StoreKey]),
		app.BankKeeper,
	)

	app.PowerKeeper = powerkeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[powertypes.StoreKey]),
		app.StakingKeeper,
		app.SlashingKeeper,
		app.BankKeeper,
		app.FeesKeeper,
	)

	app.EmissionKeeper = emissionkeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[emissiontypes.StoreKey]),
		app.BankKeeper,
		app.PowerKeeper,
		authtypes.NewModuleAddress(stakingtypes.BondedPoolName),
	)

	/****  Module Options ****/

	app.ModuleManager = module.NewManager(
		genutil.NewAppModule(app.AccountKeeper, app.StakingKeeper, app, txConfig),
		auth.NewAppModule(appCodec, app.AccountKeeper, authsims.RandomGenesisAccounts, nil),
		bank.NewAppModule(appCodec, app.BankKeeper, app.AccountKeeper, nil),
		feegrantmodule.NewAppModule(appCodec, app.AccountKeeper, app.BankKeeper, app.FeeGrantKeeper, app.interfaceRegistry),
		slashing.NewAppModule(appCodec, app.SlashingKeeper, app.AccountKeeper, app.BankKeeper, app.StakingKeeper, nil, app.interfaceRegistry),
		distr.NewAppModule(appCodec, app.DistrKeeper, app.AccountKeeper, app.BankKeeper, app.StakingKeeper, nil),
		newStakingEndBlockOverride(
			staking.NewAppModule(appCodec, app.StakingKeeper, app.AccountKeeper, app.BankKeeper, nil),
			app.StakingKeeper,
		),
		upgrade.NewAppModule(app.UpgradeKeeper, app.AccountKeeper.AddressCodec()),
		evidence.NewAppModule(app.EvidenceKeeper),
		consensus.NewAppModule(appCodec, app.ConsensusParamsKeeper),
		emission.NewAppModule(app.EmissionKeeper),
		fees.NewAppModule(app.FeesKeeper),
		power.NewAppModule(app.PowerKeeper, app.EmissionKeeper),
	)

	app.BasicModuleManager = module.NewBasicManagerFromManager(
		app.ModuleManager,
		map[string]module.AppModuleBasic{
			genutiltypes.ModuleName:  genutil.NewAppModuleBasic(genutiltypes.DefaultMessageValidator),
			banktypes.ModuleName:     bankGenesisOverride{bank.AppModuleBasic{}},
			distrtypes.ModuleName:    distrGenesisOverride{distr.AppModuleBasic{}},
			slashingtypes.ModuleName: slashingGenesisOverride{slashing.AppModuleBasic{}},
		})
	app.BasicModuleManager.RegisterLegacyAminoCodec(legacyAmino)
	app.BasicModuleManager.RegisterInterfaces(interfaceRegistry)

	app.ModuleManager.SetOrderPreBlockers(
		upgradetypes.ModuleName,
		authtypes.ModuleName,
	)
	// x/emission runs first: closing an epoch mints the validator/delegator share into its own
	// account and immediately hands it to x/power.Keeper.DistributeEpochRewards, which pays it out
	// on capped power P_i into earnings accounts (plans/open-network/track-c-chain.md C3, C4) -
	// x/distribution's own BeginBlocker still runs after it (for ordinary tx-fee sweeping from the
	// fee collector, which the custom fee ante decorator no longer feeds - see setAnteHandler), but
	// no longer receives the emission mint.
	app.ModuleManager.SetOrderBeginBlockers(
		emissiontypes.ModuleName,
		distrtypes.ModuleName,
		slashingtypes.ModuleName,
		evidencetypes.ModuleName,
		stakingtypes.ModuleName,
		genutiltypes.ModuleName,
	)
	// x/staking's (overridden) end-blocker runs before x/power's, so x/power's validator-power
	// computation for this block sees the freshest bonded set (any bonding/unbonding this block's
	// txs caused). x/fees advances the base fee once this block's gas usage is final. x/emission's
	// EndBlock runs last so it reconciles cumulative_burned against every burn any other module
	// made during the block (see keeper.Keeper.ReconcileBurns).
	app.ModuleManager.SetOrderEndBlockers(
		banktypes.ModuleName,
		stakingtypes.ModuleName,
		genutiltypes.ModuleName,
		feegrant.ModuleName,
		powertypes.ModuleName,
		feestypes.ModuleName,
		emissiontypes.ModuleName,
	)

	// NOTE: genutil must run after staking (so gentx self-delegations can bond) and after bank
	// (so genutil can read account balances); x/emission must run after bank (so a fresh genesis
	// can observe whatever supply already exists - see emissionkeeper.Keeper.InitGenesis); and
	// x/power must run last of all, after x/emission, since its InitGenesis records x/emission's
	// current epoch number as its own genesis_epoch (see power/keeper.Keeper.InitGenesis) and
	// returns the genesis CometBFT validator set (the bootstrap committee) - the only non-empty
	// InitGenesis validator-update list in this app (genutil's gentx-derived list is always empty:
	// a bootstrap-committee genesis has no gentxs - see docs/CHAIN.md).
	genesisModuleOrder := []string{
		authtypes.ModuleName,
		banktypes.ModuleName,
		distrtypes.ModuleName,
		stakingtypes.ModuleName,
		slashingtypes.ModuleName,
		genutiltypes.ModuleName,
		evidencetypes.ModuleName,
		feegrant.ModuleName,
		upgradetypes.ModuleName,
		consensusparamtypes.ModuleName,
		feestypes.ModuleName,
		emissiontypes.ModuleName,
		powertypes.ModuleName,
	}
	exportModuleOrder := []string{
		consensusparamtypes.ModuleName,
		authtypes.ModuleName,
		banktypes.ModuleName,
		distrtypes.ModuleName,
		stakingtypes.ModuleName,
		slashingtypes.ModuleName,
		genutiltypes.ModuleName,
		evidencetypes.ModuleName,
		feegrant.ModuleName,
		upgradetypes.ModuleName,
		feestypes.ModuleName,
		emissiontypes.ModuleName,
		powertypes.ModuleName,
	}

	app.ModuleManager.SetOrderInitGenesis(genesisModuleOrder...)
	app.ModuleManager.SetOrderExportGenesis(exportModuleOrder...)

	app.configurator = module.NewConfigurator(app.appCodec, app.MsgServiceRouter(), app.GRPCQueryRouter())
	if err := app.ModuleManager.RegisterServices(app.configurator); err != nil {
		panic(err)
	}

	autocliv1.RegisterQueryServer(app.GRPCQueryRouter(), runtimeservices.NewAutoCLIQueryService(app.ModuleManager.Modules))

	reflectionSvc, err := runtimeservices.NewReflectionService()
	if err != nil {
		panic(err)
	}
	reflectionv1.RegisterReflectionServiceServer(app.GRPCQueryRouter(), reflectionSvc)

	app.MountKVStores(keys)

	app.SetInitChainer(app.InitChainer)
	app.SetPreBlocker(app.PreBlocker)
	app.SetBeginBlocker(app.BeginBlocker)
	app.SetEndBlocker(app.EndBlocker)
	app.setAnteHandler(txConfig)
	app.setPostHandler()

	if loadLatest {
		if err := app.LoadLatestVersion(); err != nil {
			panic(fmt.Errorf("error loading last version: %w", err))
		}
	}

	return app
}

// setAnteHandler builds this app's ante chain by hand, matching x/auth/ante.NewAnteHandler's own
// decorator list (github.com/cosmos/cosmos-sdk/x/auth/ante@v0.54.4's NewAnteHandler) except for
// one substitution: x/fees' own FeeDecorator stands in for the stock NewDeductFeeDecorator
// (plans/open-network/track-c-chain.md C2). The stock decorator always sends the full fee to the
// fee collector for x/distribution to sweep; this chain instead burns the base-fee portion outright
// and credits the tip straight to the block proposer's earnings account, falling back to the
// payer's own earnings when their bank balance is short (see x/fees/ante.FeeDecorator).
func (app *OramaApp) setAnteHandler(txConfig client.TxConfig) {
	anteDecorators := []sdk.AnteDecorator{
		ante.NewSetUpContextDecorator(),
		ante.NewExtensionOptionsDecorator(nil),
		ante.NewValidateBasicDecorator(),
		ante.NewTxTimeoutHeightDecorator(),
		ante.NewValidateMemoDecorator(app.AccountKeeper),
		ante.NewConsumeGasForTxSizeDecorator(app.AccountKeeper),
		feesante.NewFeeDecorator(app.AccountKeeper, app.FeeGrantKeeper, app.StakingKeeper, app.FeesKeeper),
		// Security review B8 ("outsiders can never bond"): tops up a signer's own
		// MsgCreateValidator/MsgDelegate shortfall from their own earnings, before that message
		// runs - this chain starts every account at zero norama, so without it nobody outside the
		// genesis bootstrap committee could ever accumulate a public bank balance to bond with.
		feesante.NewBondTopUpDecorator(app.BankKeeper, app.FeesKeeper),
		// Security review B1/M4 ("lock the force-bonded stake"): rejects a bootstrap committee
		// member's own MsgUndelegate/MsgBeginRedelegate if it would take their self-bond below
		// what x/power has force-bonded into it, while lambda < 1.
		powerante.NewUndelegateGuard(app.StakingKeeper, app.PowerKeeper),
		// Security review M2: a delegation that would sit strictly between zero
		// and MinDelegationForRewards is rejected, so the per-epoch reward walk
		// cannot be filled with dust delegations.
		powerante.NewMinDelegationDecorator(app.StakingKeeper, app.PowerKeeper),
		ante.NewSetPubKeyDecorator(app.AccountKeeper), // must run before every signature-verification decorator
		ante.NewValidateSigCountDecorator(app.AccountKeeper),
		ante.NewSigGasConsumeDecorator(app.AccountKeeper, ante.DefaultSigVerificationGasConsumer),
		ante.NewSigVerificationDecorator(app.AccountKeeper, txConfig.SignModeHandler()),
		ante.NewIncrementSequenceDecorator(app.AccountKeeper),
	}
	app.SetAnteHandler(sdk.ChainAnteDecorators(anteDecorators...))
}

func (app *OramaApp) setPostHandler() {
	postHandler, err := posthandler.NewPostHandler(posthandler.HandlerOptions{})
	if err != nil {
		panic(err)
	}
	app.SetPostHandler(postHandler)
}

// Name returns the name of the App.
func (app *OramaApp) Name() string { return app.BaseApp.Name() }

// PreBlocker application updates every pre block.
func (app *OramaApp) PreBlocker(ctx sdk.Context, _ *abci.RequestFinalizeBlock) (*sdk.ResponsePreBlock, error) {
	return app.ModuleManager.PreBlock(ctx)
}

// BeginBlocker application updates every begin block.
func (app *OramaApp) BeginBlocker(ctx sdk.Context) (sdk.BeginBlock, error) {
	return app.ModuleManager.BeginBlock(ctx)
}

// EndBlocker application updates every end block.
func (app *OramaApp) EndBlocker(ctx sdk.Context) (sdk.EndBlock, error) {
	return app.ModuleManager.EndBlock(ctx)
}

func (app *OramaApp) Configurator() module.Configurator {
	return app.configurator
}

// InitChainer application update at chain initialization.
func (app *OramaApp) InitChainer(ctx sdk.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	var genesisState GenesisState
	if err := json.Unmarshal(req.AppStateBytes, &genesisState); err != nil {
		panic(err)
	}
	if err := app.UpgradeKeeper.SetModuleVersionMap(ctx, app.ModuleManager.GetVersionMap()); err != nil {
		return nil, err
	}
	return app.ModuleManager.InitGenesis(ctx, app.appCodec, genesisState)
}

// LoadHeight loads a particular height.
func (app *OramaApp) LoadHeight(height int64) error {
	return app.LoadVersion(height)
}

// LegacyAmino returns the app's amino codec.
func (app *OramaApp) LegacyAmino() *codec.LegacyAmino {
	return app.legacyAmino
}

// AppCodec returns the app's codec.
func (app *OramaApp) AppCodec() codec.Codec {
	return app.appCodec
}

// InterfaceRegistry returns the app's InterfaceRegistry.
func (app *OramaApp) InterfaceRegistry() types.InterfaceRegistry {
	return app.interfaceRegistry
}

// TxConfig returns the app's TxConfig.
func (app *OramaApp) TxConfig() client.TxConfig {
	return app.txConfig
}

// AutoCliOpts returns the autocli options for the app.
func (app *OramaApp) AutoCliOpts() autocli.AppOptions {
	modules := make(map[string]appmodule.AppModule, 0)
	for _, m := range app.ModuleManager.Modules {
		if moduleWithName, ok := m.(module.HasName); ok {
			moduleName := moduleWithName.Name()
			if appModule, ok := moduleWithName.(appmodule.AppModule); ok {
				modules[moduleName] = appModule
			}
		}
	}

	return autocli.AppOptions{
		Modules:               modules,
		ModuleOptions:         runtimeservices.ExtractAutoCLIOptions(app.ModuleManager.Modules),
		AddressCodec:          authcodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		ValidatorAddressCodec: authcodec.NewBech32Codec(sdk.GetConfig().GetBech32ValidatorAddrPrefix()),
		ConsensusAddressCodec: authcodec.NewBech32Codec(sdk.GetConfig().GetBech32ConsensusAddrPrefix()),
	}
}

// DefaultGenesis returns a default genesis from the registered AppModuleBasics.
func (app *OramaApp) DefaultGenesis() map[string]json.RawMessage {
	return app.BasicModuleManager.DefaultGenesis(app.appCodec)
}

// GetKey returns the KVStoreKey for the provided store key.
func (app *OramaApp) GetKey(storeKey string) *storetypes.KVStoreKey {
	return app.keys[storeKey]
}

// SimulationManager returns nil: this app does not wire the SDK's randomized simulation
// framework (plans/open-network/track-c-chain.md's own simulation requirement is covered by
// x/emission's keeper-level Go tests instead - see docs/CHAIN.md deviations).
func (app *OramaApp) SimulationManager() *module.SimulationManager {
	return nil
}

// GetStoreKeys returns all the stored store keys.
func (app *OramaApp) GetStoreKeys() []storetypes.StoreKey {
	keys := make([]storetypes.StoreKey, 0, len(app.keys))
	for _, key := range app.keys {
		keys = append(keys, key)
	}
	return keys
}

// RegisterAPIRoutes registers all application module routes with the provided API server.
func (app *OramaApp) RegisterAPIRoutes(apiSvr *api.Server, apiConfig config.APIConfig) {
	clientCtx := apiSvr.ClientCtx
	authtx.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	cmtservice.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	nodeservice.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	app.BasicModuleManager.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	if err := server.RegisterSwaggerAPI(apiSvr.ClientCtx, apiSvr.Router, apiConfig.Swagger); err != nil {
		panic(err)
	}
}

// RegisterTxService implements the Application.RegisterTxService method.
func (app *OramaApp) RegisterTxService(clientCtx client.Context) {
	authtx.RegisterTxService(app.GRPCQueryRouter(), clientCtx, app.Simulate, app.interfaceRegistry)
}

// RegisterTendermintService implements the Application.RegisterTendermintService method.
func (app *OramaApp) RegisterTendermintService(clientCtx client.Context) {
	cmtApp := server.NewCometABCIWrapper(app)
	cmtservice.RegisterTendermintService(
		clientCtx,
		app.GRPCQueryRouter(),
		app.interfaceRegistry,
		cmtApp.Query,
	)
}

// RegisterNodeService implements the Application.RegisterNodeService method.
func (app *OramaApp) RegisterNodeService(clientCtx client.Context, cfg config.Config) {
	nodeservice.RegisterNodeService(clientCtx, app.GRPCQueryRouter(), cfg, func() int64 {
		return app.CommitMultiStore().EarliestVersion()
	})
}

// GetMaccPerms returns a copy of the module account permissions.
func GetMaccPerms() map[string][]string {
	return maps.Clone(maccPerms)
}

// BlockedAddresses returns every module account address. A user cannot pay one of these
// directly. User-to-user norama sends are refused separately by the shielded send restriction.
func BlockedAddresses() map[string]bool {
	modAccAddrs := make(map[string]bool)
	for acc := range GetMaccPerms() {
		modAccAddrs[authtypes.NewModuleAddress(acc).String()] = true
	}
	return modAccAddrs
}
