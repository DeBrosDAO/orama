package keeper

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// Keeper stores upload_sunset_height, the genesis code set, deposit_per_byte and the contract
// state-deposit ledger. InitGenesis is the only writer of the first three. There is no setter and
// no Msg service. The ledger is written only by ApplyStateDelta, which the wasm engine wrapper calls.
type Keeper struct {
	fees FeesKeeper

	Schema         collections.Schema
	Sunset         collections.Item[uint64]
	Codes          collections.KeySet[uint64]
	DepositPerByte collections.Item[math.Int]
	// Chunks is keyed by (contract address bytes, sequence). Sequences only grow, so a contract's
	// newest chunk is its last row.
	Chunks        collections.Map[collections.Pair[[]byte, uint64], types.DepositChunk]
	ContractBytes collections.Map[[]byte, uint64]
	NextChunk     collections.Sequence

	uploads UploadAllowList
}

// UploadAllowList is the enacted code-upload allow-list x/houses stores after a
// structural proposal passes its timelock. wasmpolicy only reads it.
type UploadAllowList interface {
	// CodeUploadAllowed reports whether the lowercase hex SHA-256 of an
	// uncompressed wasm blob is allowed to be stored before upload_sunset_height.
	CodeUploadAllowed(ctx context.Context, sha256Hex string) (bool, error)
}

// WithUploadAllowList returns a copy of k that also lets a MsgStoreCode through
// before the sunset when the code's SHA-256 is on list. It never lets anything
// change upload_sunset_height, and after the sunset every store is allowed anyway.
func (k Keeper) WithUploadAllowList(list UploadAllowList) Keeper {
	k.uploads = list
	return k
}

// NewKeeper builds the wasmpolicy keeper on storeService. fees locks and releases the deposits.
func NewKeeper(storeService storetypes.KVStoreService, fees FeesKeeper) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		fees:           fees,
		Sunset:         collections.NewItem(sb, types.SunsetPrefix, "upload_sunset_height", collections.Uint64Value),
		Codes:          collections.NewKeySet(sb, types.CodePrefix, "genesis_code_ids", collections.Uint64Key),
		DepositPerByte: collections.NewItem(sb, types.DepositPerBytePrefix, "deposit_per_byte", sdk.IntValue),
		Chunks: collections.NewMap(
			sb, types.ChunkPrefix, "deposit_chunks",
			collections.PairKeyCodec(collections.BytesKey, collections.Uint64Key),
			types.JSONValue[types.DepositChunk]{},
		),
		ContractBytes: collections.NewMap(sb, types.ContractBytesPrefix, "contract_bytes", collections.BytesKey, collections.Uint64Value),
		NextChunk:     collections.NewSequence(sb, types.NextChunkPrefix, "next_deposit_chunk"),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// InitGenesis writes the sunset height, the genesis code set, the deposit price and the deposit
// ledger once. A second call is rejected and leaves the stored height unchanged.
func (k Keeper) InitGenesis(ctx context.Context, gs types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	has, err := k.Sunset.Has(ctx)
	if err != nil {
		return fmt.Errorf("upload_sunset_height: %w", err)
	}
	if has {
		return types.ErrSunsetImmutable
	}
	if err := k.Sunset.Set(ctx, gs.UploadSunsetHeight); err != nil {
		return fmt.Errorf("set upload_sunset_height: %w", err)
	}
	for _, id := range gs.GenesisCodeIDs {
		if err := k.Codes.Set(ctx, id); err != nil {
			return fmt.Errorf("set genesis code id %d: %w", id, err)
		}
	}
	if err := k.DepositPerByte.Set(ctx, gs.DepositPerByte); err != nil {
		return fmt.Errorf("set deposit_per_byte: %w", err)
	}
	return k.importChunks(ctx, gs.DepositChunks)
}

// ExportGenesis reads the sunset height, the genesis code set, the deposit price and the ledger.
func (k Keeper) ExportGenesis(ctx context.Context) (types.GenesisState, error) {
	height, err := k.SunsetHeight(ctx)
	if err != nil {
		return types.GenesisState{}, err
	}
	set, err := k.GenesisCodeSet(ctx)
	if err != nil {
		return types.GenesisState{}, err
	}
	ids := make([]uint64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	perByte, err := k.DepositPerByte.Get(ctx)
	if err != nil {
		return types.GenesisState{}, fmt.Errorf("deposit_per_byte: %w", err)
	}
	chunks, err := k.allChunks(ctx)
	if err != nil {
		return types.GenesisState{}, err
	}
	return types.GenesisState{UploadSunsetHeight: height, GenesisCodeIDs: ids, DepositPerByte: perByte, DepositChunks: chunks}, nil
}

// SunsetHeight returns the genesis upload_sunset_height.
func (k Keeper) SunsetHeight(ctx context.Context) (uint64, error) {
	height, err := k.Sunset.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("upload_sunset_height: %w", err)
	}
	return height, nil
}

// GenesisCodeSet returns the code ids shipped in genesis.
func (k Keeper) GenesisCodeSet(ctx context.Context) (map[uint64]struct{}, error) {
	out := map[uint64]struct{}{}
	err := k.Codes.Walk(ctx, nil, func(id uint64) (bool, error) {
		out[id] = struct{}{}
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("genesis code set: %w", err)
	}
	return out, nil
}

// CheckMsg rejects a sunset mutation, and rejects MsgStoreCode before the sunset
// unless the code id is in the genesis set. It does not write.
func (k Keeper) CheckMsg(ctx context.Context, height int64, msg sdk.Msg) error {
	store, codeID, changesSunset := classifyMsg(msg)
	if changesSunset {
		return types.ErrSunsetImmutable
	}
	if !store {
		return nil
	}
	sunset, err := k.SunsetHeight(ctx)
	if err != nil {
		return err
	}
	genesis, err := k.GenesisCodeSet(ctx)
	if err != nil {
		return err
	}
	err = AllowStore(height, sunset, codeID, genesis)
	if err == nil || !errors.Is(err, types.ErrUploadClosed) {
		return err
	}
	return k.allowByEnactedList(ctx, msg, err)
}

// allowByEnactedList lets a store through when its code hash is on the enacted
// allow-list, and otherwise returns closed (the error AllowStore gave).
func (k Keeper) allowByEnactedList(ctx context.Context, msg sdk.Msg, closed error) error {
	if k.uploads == nil {
		return closed
	}
	hash, ok := codeHash(msg)
	if !ok {
		return closed
	}
	allowed, err := k.uploads.CodeUploadAllowed(ctx, hash)
	if err != nil {
		return fmt.Errorf("upload allow-list: %w", err)
	}
	if !allowed {
		return closed
	}
	return nil
}
