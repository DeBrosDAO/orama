package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"cosmossdk.io/math"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cast"

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
		},
	)
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

// Close closes the nullifier database with the rest of the app.
func (app *OramaApp) Close() error {
	err := app.BaseApp.Close()
	if closeErr := app.nullifierStore.Close(); closeErr != nil && err == nil {
		err = closeErr
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
