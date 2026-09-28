// Package keeper implements x/storage: PRIVATE, PUBLIC_PIN and ARCHIVE deals,
// piece-proof challenges, and settlement against the storage ceiling
// (plans/open-network/track-c-chain.md C7).
package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// Keeper is x/storage's state machine. It does not import x/nodes, x/fees or
// x/emission; those show up as the interfaces on this struct.
type Keeper struct {
	cdc      codec.BinaryCodec
	bank     types.BankKeeper
	earnings types.EarningsKeeper
	deposits types.DepositKeeper
	emission types.EmissionKeeper
	nodes    types.NodeView

	Schema collections.Schema

	Params            collections.Item[types.Params]
	NextDealID        collections.Item[uint64]
	LastEpoch         collections.Item[uint64]
	LastProtocolEpoch collections.Item[uint64]
	QueueHead         collections.Item[uint64]
	QueueTail         collections.Item[uint64]
	ArchiveFund       collections.Item[math.Int]
	DealCountHeight   collections.Item[int64]
	DealsInBlock      collections.Item[uint64]

	Deals          collections.Map[uint64, types.Deal]
	Slots          collections.Map[collections.Pair[uint64, uint32], types.Slot]
	Auths          collections.Map[collections.Pair[string, string], types.DealAuthorization]
	Nodes          collections.Map[string, types.NodeState]
	Pending        collections.KeySet[uint64]
	ReplicaCount   collections.Map[string, uint64]
	ReplicaAt      collections.Map[collections.Pair[string, uint64], types.SlotRef]
	Rechallenge    collections.KeySet[collections.Pair[string, string]]
	Challenges     collections.Map[collections.Pair[uint64, string], types.ChallengeRecord]
	Queue          collections.Map[uint64, types.Settlement]
	QueuePending   collections.Map[uint64, uint64]
	Reserved       collections.Map[string, uint64]
	EpochMinted    collections.Map[uint64, math.Int]
	OperatorMinted collections.Map[collections.Pair[uint64, string], math.Int]
	Releases       collections.Map[collections.Pair[uint64, string], uint64]
	ProbationNode  collections.Map[string, uint64]
	ProbationOp    collections.Map[string, uint64]
	ProbationNet   collections.Map[string, uint64]
	ProbationASN   collections.Map[uint32, uint64]
}

// NewKeeper builds a keeper. None of the interfaces may be nil once a block runs.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	bank types.BankKeeper,
	earnings types.EarningsKeeper,
	deposits types.DepositKeeper,
	emission types.EmissionKeeper,
	nodes types.NodeView,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		cdc:               cdc,
		bank:              bank,
		earnings:          earnings,
		deposits:          deposits,
		emission:          emission,
		nodes:             nodes,
		Params:            collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		NextDealID:        collections.NewItem(sb, types.NextDealIDKey, "next_deal_id", collections.Uint64Value),
		LastEpoch:         collections.NewItem(sb, types.LastEpochKey, "last_epoch", collections.Uint64Value),
		LastProtocolEpoch: collections.NewItem(sb, types.LastProtocolEpochKey, "last_protocol_epoch", collections.Uint64Value),
		QueueHead:         collections.NewItem(sb, types.QueueHeadKey, "queue_head", collections.Uint64Value),
		QueueTail:         collections.NewItem(sb, types.QueueTailKey, "queue_tail", collections.Uint64Value),
		ArchiveFund:       collections.NewItem(sb, types.ArchiveFundKey, "archive_fund", sdk.IntValue),
		DealCountHeight:   collections.NewItem(sb, types.DealCountHeightKey, "deal_count_height", collections.Int64Value),
		DealsInBlock:      collections.NewItem(sb, types.DealsInBlockKey, "deals_in_block", collections.Uint64Value),
		Deals:             collections.NewMap(sb, types.DealsPrefix, "deals", collections.Uint64Key, codec.CollValue[types.Deal](cdc)),
		Slots:             collections.NewMap(sb, types.SlotsPrefix, "slots", collections.PairKeyCodec(collections.Uint64Key, collections.Uint32Key), codec.CollValue[types.Slot](cdc)),
		Auths:             collections.NewMap(sb, types.AuthsPrefix, "auths", collections.PairKeyCodec(collections.StringKey, collections.StringKey), codec.CollValue[types.DealAuthorization](cdc)),
		Nodes:             collections.NewMap(sb, types.NodesPrefix, "nodes", collections.StringKey, codec.CollValue[types.NodeState](cdc)),
		Pending:           collections.NewKeySet(sb, types.PendingPrefix, "pending", collections.Uint64Key),
		ReplicaCount:      collections.NewMap(sb, types.ReplicaCountPrefix, "replica_count", collections.StringKey, collections.Uint64Value),
		ReplicaAt:         collections.NewMap(sb, types.ReplicaAtPrefix, "replica_at", collections.PairKeyCodec(collections.StringKey, collections.Uint64Key), codec.CollValue[types.SlotRef](cdc)),
		Rechallenge:       collections.NewKeySet(sb, types.RechallengePrefix, "rechallenge", collections.PairKeyCodec(collections.StringKey, collections.StringKey)),
		Challenges:        collections.NewMap(sb, types.ChallengesPrefix, "challenges", collections.PairKeyCodec(collections.Uint64Key, collections.StringKey), codec.CollValue[types.ChallengeRecord](cdc)),
		Queue:             collections.NewMap(sb, types.QueuePrefix, "queue", collections.Uint64Key, codec.CollValue[types.Settlement](cdc)),
		QueuePending:      collections.NewMap(sb, types.QueuePendingPrefix, "queue_pending", collections.Uint64Key, collections.Uint64Value),
		Reserved:          collections.NewMap(sb, types.ReservedPrefix, "reserved", collections.StringKey, collections.Uint64Value),
		EpochMinted:       collections.NewMap(sb, types.EpochMintPrefix, "epoch_minted", collections.Uint64Key, sdk.IntValue),
		OperatorMinted:    collections.NewMap(sb, types.OperatorMintPrefix, "operator_minted", collections.PairKeyCodec(collections.Uint64Key, collections.StringKey), sdk.IntValue),
		Releases:          collections.NewMap(sb, types.ReleasesPrefix, "releases", collections.PairKeyCodec(collections.Uint64Key, collections.StringKey), collections.Uint64Value),
		ProbationNode:     collections.NewMap(sb, types.ProbationNodePrefix, "probation_node", collections.StringKey, collections.Uint64Value),
		ProbationOp:       collections.NewMap(sb, types.ProbationOpPrefix, "probation_op", collections.StringKey, collections.Uint64Value),
		ProbationNet:      collections.NewMap(sb, types.ProbationNetPrefix, "probation_net", collections.StringKey, collections.Uint64Value),
		ProbationASN:      collections.NewMap(sb, types.ProbationASNPrefix, "probation_asn", collections.Uint32Key, collections.Uint64Value),
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

func (k Keeper) params(ctx sdk.Context) (types.Params, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return types.Params{}, fmt.Errorf("failed to load storage params: %w", err)
	}
	return p, nil
}

func (k Keeper) currentEpoch(ctx sdk.Context) (uint64, error) {
	epoch, err := k.emission.CurrentEpoch(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to read emission epoch: %w", err)
	}
	return epoch, nil
}
