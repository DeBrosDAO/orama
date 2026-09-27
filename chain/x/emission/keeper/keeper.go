// Package keeper implements x/emission's state machine: the ossified halving-with-tail schedule
// described in plans/open-network.md ("Supply and emission") and
// plans/open-network/track-c-chain.md (C3).
package keeper

import (
	"context"

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

	// feeCollectorName is the module account name the validator/delegator share is minted into,
	// so x/distribution's own BeginBlocker pays it out on capped power.
	feeCollectorName string
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
	feeCollectorName string,
	bondedPoolAddr sdk.AccAddress,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		storeService:     storeService,
		bankKeeper:       bankKeeper,
		feeCollectorName: feeCollectorName,
		bondedPoolAddr:   bondedPoolAddr,
		Params:           collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		EpochState:       collections.NewItem(sb, types.EpochStateKey, "epoch_state", codec.CollValue[types.EpochState](cdc)),
		Ceilings:         collections.NewMap(sb, types.CeilingsPrefix, "ceilings", collections.Uint64Key, codec.CollValue[types.CeilingRecord](cdc)),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	return k
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}
