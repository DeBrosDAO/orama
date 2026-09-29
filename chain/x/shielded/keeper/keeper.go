// Package keeper implements x/shielded: the note-commitment tree frontier and anchors, the
// per-vintage per-asset pool balances and turnstiles, the 24h unshield cap and queue, and the
// nullifier set (plans/open-network/track-c-chain.md C12). There is no authority address, no
// pause and no way to switch the pool off (plans/open-network.md D20).
package keeper

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// errStopWalk ends a nullifier walk early once it has read what it needs.
var errStopWalk = errors.New("stop nullifier walk")

// Dependencies are what the keeper is wired to. Every field is required.
type Dependencies struct {
	Bank       types.BankKeeper
	Fees       types.FeesKeeper
	Bonder     types.Bonder
	NodeBonder types.NodeBonder
	Tree       types.Tree
	Nullifiers types.NullifierStore
	// Verifiers must all accept a bundle (verify.Check). Fewer than verify.MinVerifiers, or a
	// verifier that is not linked, means no bundle is ever accepted.
	Verifiers []verify.Verifier
}

// Keeper is x/shielded's keeper.
type Keeper struct {
	deps Dependencies

	emptyRoot *emptyRootCache

	Schema         collections.Schema
	Params         collections.Item[types.Params]
	Pools          collections.Map[collections.Pair[uint32, []byte], math.Int]
	Limiters       collections.Map[collections.Pair[uint32, []byte], types.Limiter]
	Queue          collections.Map[uint64, types.QueuedUnshield]
	NextQueueID    collections.Sequence
	Frontier       collections.Item[[]byte]
	TreeSize       collections.Item[uint64]
	CurrentRoot    collections.Item[[]byte]
	Anchors        collections.Map[[]byte, int64]
	AnchorHeights  collections.Map[int64, []byte]
	Accumulator    collections.Item[[]byte]
	NullifierCount collections.Item[uint64]

	pendingSchema collections.Schema
	pendingList   collections.Map[uint64, []byte]
	pendingSet    collections.KeySet[[]byte]
	pendingSeq    collections.Sequence
}

// emptyRootCache computes the empty tree's root once. The Sinsemilla hashing is not free.
type emptyRootCache struct {
	once sync.Once
	root [bundle.NodeLen]byte
	err  error
}

// NewKeeper builds a keeper. store is the module's IAVL store, transient its transient store.
func NewKeeper(
	cdc codec.BinaryCodec,
	store storetypes.KVStoreService,
	transient storetypes.KVStoreService,
	deps Dependencies,
) Keeper {
	if deps.Bank == nil || deps.Fees == nil || deps.Bonder == nil || deps.NodeBonder == nil ||
		deps.Tree == nil || deps.Nullifiers == nil {
		panic("x/shielded: every dependency is required")
	}
	poolKey := collections.PairKeyCodec(collections.Uint32Key, collections.BytesKey)
	sb := collections.NewSchemaBuilder(store)
	k := Keeper{
		deps:      deps,
		emptyRoot: &emptyRootCache{},
		Params:    collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Pools:     collections.NewMap(sb, types.PoolsPrefix, "pools", poolKey, sdk.IntValue),
		Limiters:  collections.NewMap(sb, types.LimitersPrefix, "limiters", poolKey, codec.CollValue[types.Limiter](cdc)),
		Queue:     collections.NewMap(sb, types.QueuePrefix, "queue", collections.Uint64Key, codec.CollValue[types.QueuedUnshield](cdc)),

		NextQueueID:    collections.NewSequence(sb, types.NextQueueIDPrefix, "next_queue_id"),
		Frontier:       collections.NewItem(sb, types.FrontierKey, "frontier", collections.BytesValue),
		TreeSize:       collections.NewItem(sb, types.TreeSizeKey, "tree_size", collections.Uint64Value),
		CurrentRoot:    collections.NewItem(sb, types.CurrentRootKey, "current_root", collections.BytesValue),
		Anchors:        collections.NewMap(sb, types.AnchorsPrefix, "anchors", collections.BytesKey, collections.Int64Value),
		AnchorHeights:  collections.NewMap(sb, types.AnchorHeightPrefix, "anchor_heights", collections.Int64Key, collections.BytesValue),
		Accumulator:    collections.NewItem(sb, types.AccumulatorKey, "nullifier_accumulator", collections.BytesValue),
		NullifierCount: collections.NewItem(sb, types.NullifierCountKey, "nullifier_count", collections.Uint64Value),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	k.initPending(transient)
	return k
}

// initPending builds the collections of the pending-nullifier transient store.
func (k *Keeper) initPending(transient storetypes.KVStoreService) {
	tb := collections.NewSchemaBuilder(transient)
	k.pendingList = collections.NewMap(tb, types.PendingListPrefix, "pending_list", collections.Uint64Key, collections.BytesValue)
	k.pendingSet = collections.NewKeySet(tb, types.PendingSetPrefix, "pending_set", collections.BytesKey)
	k.pendingSeq = collections.NewSequence(tb, types.PendingSeqPrefix, "pending_seq")
	schema, err := tb.Build()
	if err != nil {
		panic(err)
	}
	k.pendingSchema = schema
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	return sdk.UnwrapSDKContext(ctx).Logger().With("module", "x/"+types.ModuleName)
}

func (k Keeper) params(ctx context.Context) (types.Params, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return types.Params{}, fmt.Errorf("load x/shielded params: %w", err)
	}
	return p, nil
}

// getBytes reads an optional bytes item, nil when it was never set.
func getBytes(ctx context.Context, item collections.Item[[]byte]) ([]byte, error) {
	v, err := item.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, nil
	}
	return v, err
}

func getUint64(ctx context.Context, item collections.Item[uint64]) (uint64, error) {
	v, err := item.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return 0, nil
	}
	return v, err
}

// EmptyRoot is the root of a tree with no notes.
func (k Keeper) EmptyRoot() ([bundle.NodeLen]byte, error) {
	c := k.emptyRoot
	c.once.Do(func() { c.root, c.err = k.deps.Tree.EmptyRoot() })
	return c.root, c.err
}

// TransientKVService lets the collections of the pending-nullifier store sit on a transient store.
func TransientKVService(t storetypes.TransientStoreService) storetypes.KVStoreService {
	return transientAsKV{t}
}

type transientAsKV struct {
	storetypes.TransientStoreService
}

func (t transientAsKV) OpenKVStore(ctx context.Context) storetypes.KVStore {
	return t.OpenTransientStore(ctx)
}
