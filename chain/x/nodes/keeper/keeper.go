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
	cdc            codec.BinaryCodec
	storeService   storetypes.KVStoreService
	bankKeeper     types.BankKeeper
	depositKeeper  types.DepositKeeper
	earningsKeeper types.EarningsKeeper
	accountKeeper  types.AccountKeeper

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
	StorageDirty    collections.KeySet[string]
	// HotKeys maps the hot key of every live node to its node id, and LiveIPs maps every literal
	// IP among a live node's endpoints to its node id. Both are derived from Nodes and rebuilt
	// from genesis; neither is exported.
	HotKeys collections.Map[string, string]
	LiveIPs collections.Map[string, string]
	// Names maps every claimed identification name to its claim, and NodeNames maps a node id to
	// the name it holds. Both are exported through the genesis node_names list.
	Names     collections.Map[string, types.NodeName]
	NodeNames collections.Map[string, string]
}

// NewKeeper builds a keeper. bankKeeper escrows bonds; depositKeeper locks
// the C2 state deposit for node and cluster records; earningsKeeper moves an
// operator's earnings to its node's hot key. All three are required.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	bankKeeper types.BankKeeper,
	depositKeeper types.DepositKeeper,
	earningsKeeper types.EarningsKeeper,
	accountKeeper types.AccountKeeper,
) Keeper {
	if bankKeeper == nil {
		panic("x/nodes bank keeper is nil")
	}
	if depositKeeper == nil {
		panic("x/nodes deposit keeper is nil")
	}
	if earningsKeeper == nil {
		panic("x/nodes earnings keeper is nil")
	}
	if accountKeeper == nil {
		panic("x/nodes account keeper is nil")
	}
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		cdc:            cdc,
		storeService:   storeService,
		bankKeeper:     bankKeeper,
		depositKeeper:  depositKeeper,
		earningsKeeper: earningsKeeper,
		accountKeeper:  accountKeeper,
		Params:         collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Operators:      collections.NewMap(sb, types.OperatorPrefix, "operators", collections.StringKey, codec.CollValue[types.Operator](cdc)),
		Nodes:          collections.NewMap(sb, types.NodePrefix, "nodes", collections.StringKey, codec.CollValue[types.Node](cdc)),
		Clusters:       collections.NewMap(sb, types.ClusterPrefix, "clusters", collections.StringKey, codec.CollValue[types.Cluster](cdc)),
		Unbondings:     collections.NewMap(sb, types.UnbondingPrefix, "unbondings", collections.Uint64Key, codec.CollValue[types.UnbondingEntry](cdc)),
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
		StorageDirty: collections.NewKeySet(sb, types.StorageDirtyPrefix, "storage_dirty", collections.StringKey),
		HotKeys:      collections.NewMap(sb, types.HotKeyPrefix, "hot_keys", collections.StringKey, collections.StringValue),
		LiveIPs:      collections.NewMap(sb, types.LiveIPPrefix, "live_ips", collections.StringKey, collections.StringValue),
		Names:        collections.NewMap(sb, types.NameOwnerPrefix, "node_names", collections.StringKey, codec.CollValue[types.NodeName](cdc)),
		NodeNames:    collections.NewMap(sb, types.NodeNamePrefix, "node_name_of", collections.StringKey, collections.StringValue),
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
