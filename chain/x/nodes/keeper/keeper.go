// Package keeper implements x/nodes: operators, global nodes, role bonds,
// service-key bindings, the unbonding queue, and the optional cluster registry
// (plans/open-network/track-c-chain.md C6). There is no authority address.
package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// Keeper is x/nodes' keeper.
type Keeper struct {
	cdc           codec.BinaryCodec
	storeService  storetypes.KVStoreService
	bankKeeper    types.BankKeeper
	depositKeeper types.DepositKeeper

	Schema          collections.Schema
	Params          collections.Item[types.Params]
	Operators       collections.Map[string, types.Operator]
	Nodes           collections.Map[string, types.Node]
	Clusters        collections.Map[string, types.Cluster]
	Unbondings      collections.Map[uint64, types.UnbondingEntry]
	UnbondingByTime collections.Map[collections.Pair[int64, uint64], uint64]
	UnbondingByNode collections.Map[collections.Pair[string, uint64], uint64]
	NextUnbonding   collections.Sequence
	Revoked         collections.Map[string, types.RevokedPubkey]
	LivePubkeys     collections.Map[string, string]
	ServiceDays     collections.Map[collections.Pair[string, uint64], types.ServiceDay]
	FreeCapacity    collections.Map[collections.Triple[uint32, string, string], uint64]
}

// NewKeeper builds a keeper. bankKeeper escrows bonds; depositKeeper locks
// the C2 state deposit for node and cluster records. Both are required.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	bankKeeper types.BankKeeper,
	depositKeeper types.DepositKeeper,
) Keeper {
	if bankKeeper == nil {
		panic("x/nodes bank keeper is nil")
	}
	if depositKeeper == nil {
		panic("x/nodes deposit keeper is nil")
	}
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		cdc:           cdc,
		storeService:  storeService,
		bankKeeper:    bankKeeper,
		depositKeeper: depositKeeper,
		Params:        collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Operators:     collections.NewMap(sb, types.OperatorPrefix, "operators", collections.StringKey, codec.CollValue[types.Operator](cdc)),
		Nodes:         collections.NewMap(sb, types.NodePrefix, "nodes", collections.StringKey, codec.CollValue[types.Node](cdc)),
		Clusters:      collections.NewMap(sb, types.ClusterPrefix, "clusters", collections.StringKey, codec.CollValue[types.Cluster](cdc)),
		Unbondings:    collections.NewMap(sb, types.UnbondingPrefix, "unbondings", collections.Uint64Key, codec.CollValue[types.UnbondingEntry](cdc)),
		UnbondingByTime: collections.NewMap(
			sb, types.UnbondingTimePrefix, "unbonding_by_time",
			collections.PairKeyCodec(collections.Int64Key, collections.Uint64Key),
			collections.Uint64Value,
		),
		UnbondingByNode: collections.NewMap(
			sb, types.UnbondingNodePrefix, "unbonding_by_node",
			collections.PairKeyCodec(collections.StringKey, collections.Uint64Key),
			collections.Uint64Value,
		),
		NextUnbonding: collections.NewSequence(sb, types.NextUnbondingPrefix, "next_unbonding"),
		Revoked:       collections.NewMap(sb, types.RevokedPrefix, "revoked_pubkeys", collections.StringKey, codec.CollValue[types.RevokedPubkey](cdc)),
		LivePubkeys:   collections.NewMap(sb, types.LivePubkeyPrefix, "live_pubkeys", collections.StringKey, collections.StringValue),
		ServiceDays: collections.NewMap(
			sb, types.ServiceDayPrefix, "service_days",
			collections.PairKeyCodec(collections.StringKey, collections.Uint64Key),
			codec.CollValue[types.ServiceDay](cdc),
		),
		FreeCapacity: collections.NewMap(
			sb, types.FreeCapacityPrefix, "free_capacity",
			collections.TripleKeyCodec(collections.Uint32Key, collections.StringKey, collections.StringKey),
			collections.Uint64Value,
		),
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

func (k Keeper) transact(ctx sdk.Context, fn func(sdk.Context) error) error {
	cache, write := ctx.CacheContext()
	if err := fn(cache); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) params(ctx sdk.Context) (types.Params, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return types.Params{}, fmt.Errorf("load x/nodes params: %w", err)
	}
	return p, nil
}
