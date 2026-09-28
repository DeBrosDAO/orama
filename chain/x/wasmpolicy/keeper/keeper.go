package keeper

import (
	"context"
	"fmt"
	"sort"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// Keeper stores upload_sunset_height and the genesis code set.
// InitGenesis is the only writer. There is no setter and no Msg service.
type Keeper struct {
	Schema collections.Schema
	Sunset collections.Item[uint64]
	Codes  collections.KeySet[uint64]
}

// NewKeeper builds the wasmpolicy keeper on storeService.
func NewKeeper(storeService storetypes.KVStoreService) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		Sunset: collections.NewItem(sb, types.SunsetPrefix, "upload_sunset_height", collections.Uint64Value),
		Codes:  collections.NewKeySet(sb, types.CodePrefix, "genesis_code_ids", collections.Uint64Key),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// InitGenesis writes the sunset height and genesis code set once.
// A second call is rejected and leaves the stored height unchanged.
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
	return nil
}

// ExportGenesis reads the sunset height and genesis code set.
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
	return types.GenesisState{UploadSunsetHeight: height, GenesisCodeIDs: ids}, nil
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
	return AllowStore(height, sunset, codeID, genesis)
}
