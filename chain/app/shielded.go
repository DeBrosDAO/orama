package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cast"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/server"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	feesante "github.com/DeBrosOfficial/network/chain/x/fees/ante"
	feeskeeper "github.com/DeBrosOfficial/network/chain/x/fees/keeper"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	powerkeeper "github.com/DeBrosOfficial/network/chain/x/power/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	shieldedkeeper "github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
	shieldedsnapshot "github.com/DeBrosOfficial/network/chain/x/shielded/snapshot"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
	orchardverify "github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
)

// openNullifierStore opens the nullifier database under <home>/data, next to application.db, so
// resetting the node's data resets it too. An app without a home (tests) gets a memory database.
func openNullifierStore(appOpts servertypes.AppOptions) (*nullifier.Store, error) {
	home := cast.ToString(appOpts.Get(flags.FlagHome))
	if home == "" {
		return nullifier.NewStore(dbm.NewMemDB()), nil
	}
	dir := filepath.Join(home, "data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	db, err := dbm.NewDB(shieldedtypes.NullifierStoreFile, server.GetAppDBBackend(appOpts), dir)
	if err != nil {
		return nil, fmt.Errorf("open the nullifier database in %s: %w", dir, err)
	}
	return nullifier.NewStore(db), nil
}

// shieldedFees adapts x/fees to the shielded module's needs.
type shieldedFees struct {
	feeskeeper.Keeper
	staking feesante.StakingKeeper
}

func (f shieldedFees) BaseFeePerGas(ctx context.Context) (math.Int, error) { return f.BaseFee.Get(ctx) }

func (shieldedFees) EarningsModule() string { return feestypes.ModuleName }

func (f shieldedFees) ProposerAccount(ctx sdk.Context) (sdk.AccAddress, bool) {
	return feesante.ResolveProposer(ctx, f.staking)
}

// shieldedBonder delegates through x/staking's own message server, so every staking rule applies.
// It also applies the rule x/power's MinDelegationDecorator applies to a signed MsgDelegate, which
// a delegation made from the shielded module would otherwise skip: a delegation may not end up
// strictly between zero and MinDelegationForRewards, or dust delegations could fill the per-epoch
// reward walk.
type shieldedBonder struct {
	staking *stakingkeeper.Keeper
	power   powerkeeper.Keeper
}

func (b shieldedBonder) Delegate(ctx sdk.Context, delegator sdk.AccAddress, validator string, amount math.Int) error {
	if err := b.CheckMinimum(ctx, delegator, validator, amount); err != nil {
		return err
	}
	bondDenom, err := b.staking.BondDenom(ctx)
	if err != nil {
		return fmt.Errorf("load the bond denom: %w", err)
	}
	_, err = stakingkeeper.NewMsgServerImpl(b.staking).Delegate(ctx, &stakingtypes.MsgDelegate{
		DelegatorAddress: delegator.String(),
		ValidatorAddress: validator,
		Amount:           sdk.NewCoin(bondDenom, amount),
	})
	return err
}

// CheckMinimum refuses a delegation that would leave the delegator's stake with the validator
// below x/power's minimum for rewards.
func (b shieldedBonder) CheckMinimum(ctx sdk.Context, delegator sdk.AccAddress, validator string, amount math.Int) error {
	p, err := b.power.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("load power params: %w", err)
	}
	valAddr, err := sdk.ValAddressFromBech32(validator)
	if err != nil {
		return fmt.Errorf("validator %q: %w", validator, err)
	}
	if _, err := b.staking.GetValidator(ctx, valAddr); err != nil {
		return fmt.Errorf("validator %s does not exist: %w", validator, err)
	}
	if p.MinDelegationForRewards.IsNil() || !p.MinDelegationForRewards.IsPositive() {
		return nil
	}
	total := amount
	delegation, err := b.staking.GetDelegation(ctx, delegator, valAddr)
	switch {
	case err == nil:
		val, err := b.staking.GetValidator(ctx, valAddr)
		if err != nil {
			return fmt.Errorf("load validator %s: %w", validator, err)
		}
		total = total.Add(val.TokensFromShares(delegation.Shares).TruncateInt())
	case !errors.Is(err, stakingtypes.ErrNoDelegation):
		return fmt.Errorf("load %s's delegation to %s: %w", delegator, validator, err)
	}
	if total.LT(p.MinDelegationForRewards) {
		return fmt.Errorf("a delegation of %s%s would be below the %s%s minimum that earns rewards",
			total, params.BaseDenom, p.MinDelegationForRewards, params.BaseDenom)
	}
	return nil
}

// buildShielded wires x/shielded: the nullifier database, the two verifiers and the keeper.
func (app *OramaApp) buildShielded(keys map[string]*storetypes.KVStoreKey, tkeys map[string]*storetypes.TransientStoreKey, appOpts servertypes.AppOptions) {
	store, err := openNullifierStore(appOpts)
	if err != nil {
		panic(err)
	}
	app.nullifierStore = store

	app.ShieldedVerifiers = app.newShieldedVerifiers(appOpts)
	app.ShieldedMempoolVerifiers, app.ShieldedProposalVerifiers = app.newChildVerifiers(app.ShieldedVerifiers, appOpts)
	app.AddRunTxRecoveryHandler(verifierHaltHandler(app.Logger(), func(code int) { VerifierHaltExit(code) }))
	app.ShieldedKeeper = shieldedkeeper.NewKeeper(
		app.appCodec,
		runtime.NewKVStoreService(keys[shieldedtypes.StoreKey]),
		shieldedkeeper.TransientKVService(runtime.NewTransientStoreService(tkeys[shieldedtypes.TransientKey])),
		shieldedkeeper.Dependencies{
			Bank:       app.BankKeeper,
			Fees:       shieldedFees{Keeper: app.FeesKeeper, staking: app.StakingKeeper},
			Bonder:     shieldedBonder{staking: app.StakingKeeper, power: app.PowerKeeper},
			NodeBonder: app.NodesKeeper,
			Tree:       orchardverify.NewTree(),
			Nullifiers: store,
			Verifiers:  app.ShieldedVerifiers,
			// CheckTx and simulations have a verifier process of their own (see newMempoolVerifiers).
			MempoolVerifiers: app.ShieldedMempoolVerifiers,
			// ProcessProposal and PrepareProposal have a child of their own.
			ProposalVerifiers: app.ShieldedProposalVerifiers,
			// A chain that is not a localnet or a scripted devnet must verify, so a verifier that
			// is missing at run time halts the node instead of refusing bundles on it alone.
			RequireVerifiers: !verifiersOptional(app.ChainID()),
		},
	)
}

// ExitVerifierHalt is the exit status of an oramad that stopped because its shielded verifier
// failed in a block. It is distinct so the journal and a monitor can tell it from a crash; the chain
// unit's Restart=always restarts it whatever the status, and the restarted node executes the block
// again (core/pkg/install global_units.go).
const ExitVerifierHalt = 87

// VerifierHaltExit ends the process. Only tests replace it.
var VerifierHaltExit = os.Exit

// verifierHaltHandler is the transaction runner's recovery handler for a shielded verifier halt.
// The runner turns any panic in a transaction into that transaction's failed result, which is right
// for a bug in a message and wrong here: a verifier fault is a fact about this node's machine, and a
// result recorded from it would differ from its peers' (shieldedkeeper.VerifierHalt). The handler
// logs the halt with the height and the bundle's hash and ends the process. It does not rely on the
// panic alone, because CometBFT's consensus routine recovers a panic, logs it and stops consensus
// but leaves the process alive, so systemd would never restart a node that no longer commits. If
// exit returns (a test), the handler panics again with the halt itself.
func verifierHaltHandler(logger log.Logger, exit func(int)) baseapp.RecoveryHandler {
	return func(recovered any) error {
		halt, ok := recovered.(shieldedkeeper.VerifierHalt)
		if !ok {
			return nil
		}
		logger.Error("STOPPING THE NODE: "+halt.Error(), "module", "x/shielded", "height", halt.Height, "bundle_sha256", halt.BundleHash)
		exit(ExitVerifierHalt)
		panic(halt)
	}
}

// registerShieldedSnapshot adds the nullifier database to state-sync snapshots. The IAVL snapshot
// carries only the accumulator; without this a state-synced node would have an empty set.
func (app *OramaApp) registerShieldedSnapshot() {
	manager := app.SnapshotManager()
	if manager == nil {
		return
	}
	committed := func(height uint64) ([bundle.NodeLen]byte, uint64, error) {
		cms, err := app.CommitMultiStore().CacheMultiStoreWithVersion(int64(height))
		if err != nil {
			return [bundle.NodeLen]byte{}, 0, fmt.Errorf("open the state at height %d: %w", height, err)
		}
		ctx := sdk.NewContext(cms, cmtproto.Header{Height: int64(height)}, false, app.Logger())
		return app.ShieldedKeeper.NullifierState(ctx)
	}
	if err := manager.RegisterExtensions(shieldedsnapshot.New(app.nullifierStore, committed)); err != nil {
		panic(fmt.Errorf("register the nullifier snapshot extension: %w", err))
	}
}

// NullifierStore is the node's nullifier database. Offline tooling that writes a chain's state
// without blocks (cmd/oramad-state's state generator) records nullifiers through it; consensus code
// never reads it from here.
func (app *OramaApp) NullifierStore() *nullifier.Store { return app.nullifierStore }

// Close closes the nullifier database with the rest of the app, releases the wasm VM (and its lock
// on <home>/wasm), and removes the VM's directory when the app made one for itself (an app with no
// home).
func (app *OramaApp) Close() error {
	err := app.BaseApp.Close()
	// The verifier processes end with the app: a node that is closed and opened again in one
	// process (a test, an upgrade) must not leave a child behind for each open.
	for _, set := range [][]verify.Verifier{app.ShieldedVerifiers, app.ShieldedMempoolVerifiers, app.ShieldedProposalVerifiers} {
		for _, v := range set {
			if c, ok := v.(io.Closer); ok {
				if closeErr := c.Close(); closeErr != nil && err == nil {
					err = closeErr
				}
			}
		}
	}
	if closeErr := app.nullifierStore.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if app.wasmVM != nil {
		app.wasmVM.Cleanup()
		app.wasmVM = nil
	}
	if app.wasmTempHome != "" {
		if rmErr := os.RemoveAll(app.wasmTempHome); rmErr != nil && err == nil {
			err = fmt.Errorf("remove the wasm VM directory %s: %w", app.wasmTempHome, rmErr)
		}
	}
	return err
}

// checkShieldedStoreAtStart refuses to start a node whose nullifier database does not fold to the
// accumulator and count the committed state holds. Such a node would accept a spent nullifier or
// refuse a fresh one, and diverge from the network on the first shielded bundle.
func (app *OramaApp) checkShieldedStoreAtStart() {
	height := app.LastBlockHeight()
	if height == 0 {
		return
	}
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: height})
	if err := app.ShieldedKeeper.CheckNullifierStore(ctx); err != nil {
		panic(fmt.Errorf("the shielded nullifier database does not match the chain state at height %d: %w; "+
			"it lives in <home>/data/shielded_nullifiers.db and must be restored together with application.db "+
			"(or state-synced with the snapshot extension); to rebuild, reset the node's data and sync again", height, err))
	}
}
