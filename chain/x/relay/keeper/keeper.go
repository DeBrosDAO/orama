// Package keeper implements x/relay's state machine: reporter-median relay
// rewards minted against x/emission's relay ceiling and paid into earnings
// (plans/open-network/track-c-chain.md C8).
package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// Keeper is x/relay's keeper. It has no admin key. The reporter set changes
// only through MsgUpdateReporters, and only when the caller has set
// AllowReporterChange on the context. Jailing happens only through JailRelay.
type Keeper struct {
	storeService storetypes.KVStoreService
	nodes        types.NodeView
	emission     types.EmissionKeeper
	earnings     types.EarningsKeeper

	Schema       collections.Schema
	Params       collections.Item[types.Params]
	Reporters    collections.Map[string, bool]
	Relays       collections.Map[[]byte, types.Relay]
	NodeIndex    collections.Map[string, []byte]
	Chunks       collections.Map[collections.Triple[uint64, string, uint32], types.ReportChunk]
	Reports      collections.Map[collections.Pair[uint64, string], types.CompleteReport]
	Activation   collections.Item[types.Activation]
	EpochResults collections.Map[uint64, types.EpochResult]
	Payouts      collections.Map[collections.Pair[uint64, []byte], types.RelayPayout]
}

// NewKeeper builds a new x/relay Keeper.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	nodes types.NodeView,
	emission types.EmissionKeeper,
	earnings types.EarningsKeeper,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		storeService: storeService,
		nodes:        nodes,
		emission:     emission,
		earnings:     earnings,
		Params:       collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Reporters:    collections.NewMap(sb, types.ReportersPrefix, "reporters", collections.StringKey, collections.BoolValue),
		Relays:       collections.NewMap(sb, types.RelaysPrefix, "relays", collections.BytesKey, codec.CollValue[types.Relay](cdc)),
		NodeIndex:    collections.NewMap(sb, types.NodeIndexPrefix, "node_index", collections.StringKey, collections.BytesValue),
		Chunks: collections.NewMap(
			sb, types.ChunksPrefix, "chunks",
			collections.TripleKeyCodec(collections.Uint64Key, collections.StringKey, collections.Uint32Key),
			codec.CollValue[types.ReportChunk](cdc),
		),
		Reports: collections.NewMap(
			sb, types.ReportsPrefix, "reports",
			collections.PairKeyCodec(collections.Uint64Key, collections.StringKey),
			codec.CollValue[types.CompleteReport](cdc),
		),
		Activation:   collections.NewItem(sb, types.ActivationKey, "activation", codec.CollValue[types.Activation](cdc)),
		EpochResults: collections.NewMap(sb, types.EpochResultsPrefix, "epoch_results", collections.Uint64Key, codec.CollValue[types.EpochResult](cdc)),
		Payouts: collections.NewMap(
			sb, types.PayoutsPrefix, "payouts",
			collections.PairKeyCodec(collections.Uint64Key, collections.BytesKey),
			codec.CollValue[types.RelayPayout](cdc),
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

func (k Keeper) getRelay(ctx context.Context, fingerprint []byte) (types.Relay, bool, error) {
	relay, err := k.Relays.Get(ctx, fingerprint)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Relay{}, false, nil
		}
		return types.Relay{}, false, fmt.Errorf("failed to load relay: %w", err)
	}
	return relay, true, nil
}
