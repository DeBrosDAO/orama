// Package keeper implements x/emission's state machine: the ossified halving-with-tail schedule
// described in plans/open-network.md ("Supply and emission") and
// plans/open-network/track-c-chain.md (C3).
package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// Keeper is x/emission's keeper. It has no authority address and no Msg service: nothing can ever
// change the emission schedule or its genesis parameters after genesis (plans/open-network.md
// D18).
type Keeper struct {
	storeService storetypes.KVStoreService
	bankKeeper   types.BankKeeper
	powerKeeper  types.PowerKeeper
	splits       types.SplitSource

	// bondedPoolAddr is x/staking's bonded-pool module account address. It is used only by the
	// devnet-only bootstrap-stake premine gate in InitGenesis, to check that genesis supply sits
	// entirely in the bonded pool rather than idle in a plain account.
	bondedPoolAddr sdk.AccAddress

	Schema     collections.Schema
	Params     collections.Item[types.Params]
	EpochState collections.Item[types.EpochState]
	// Ceilings is the bounded trailing window (types.CeilingWindow epochs) of non-minted per-epoch
	// storage/relay/development ceiling records, keyed by epoch number.
	Ceilings collections.Map[uint64, types.CeilingRecord]
}

// NewKeeper builds a new x/emission Keeper.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	bankKeeper types.BankKeeper,
	powerKeeper types.PowerKeeper,
	bondedPoolAddr sdk.AccAddress,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		storeService:   storeService,
		bankKeeper:     bankKeeper,
		powerKeeper:    powerKeeper,
		bondedPoolAddr: bondedPoolAddr,
		Params:         collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		EpochState:     collections.NewItem(sb, types.EpochStateKey, "epoch_state", codec.CollValue[types.EpochState](cdc)),
		Ceilings:       collections.NewMap(sb, types.CeilingsPrefix, "ceilings", collections.Uint64Key, codec.CollValue[types.CeilingRecord](cdc)),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	return k
}

// WithSplitSource returns a copy of k that closes epochs at the split src reports. x/houses
// stores the split a structural proposal enacts; without a source every epoch closes at the
// canonical 60/25/10/5. Every copy of the keeper that closes epochs must be built from the
// returned value.
func (k Keeper) WithSplitSource(src types.SplitSource) Keeper {
	k.splits = src
	return k
}

// currentSplit is the split in force for an epoch that closes now.
func (k Keeper) currentSplit(ctx context.Context) (types.SplitPercents, error) {
	if k.splits == nil {
		return types.CanonicalSplitPercents(), nil
	}
	pct, err := k.splits.EmissionSplit(ctx)
	if err != nil {
		return types.SplitPercents{}, fmt.Errorf("failed to load the enacted emission split: %w", err)
	}
	if err := pct.Validate(); err != nil {
		return types.SplitPercents{}, fmt.Errorf("enacted emission split is invalid: %w", err)
	}
	return pct, nil
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}

// CurrentEpoch returns the epoch number currently in progress. It implements
// power/types.EmissionKeeper, so x/power can measure its own time-based rules (the bootstrap
// deadline, the cap hysteresis window, the new-validator ramp) in the same epoch units
// x/emission's schedule uses (see docs/CHAIN.md).
func (k Keeper) CurrentEpoch(ctx context.Context) (uint64, error) {
	state, err := k.EpochState.Get(ctx)
	if err != nil {
		return 0, err
	}
	return state.CurrentEpoch, nil
}
