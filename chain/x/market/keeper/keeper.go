// Package keeper implements x/market: listings, bids and settlement. Sale
// proceeds and royalties are credited to earnings accounts.
package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	cnftkeeper "github.com/DeBrosOfficial/network/chain/x/cnft/keeper"
	"github.com/DeBrosOfficial/network/chain/x/market/types"
)

// Keeper is x/market's keeper. It has no authority address.
type Keeper struct {
	storeService storetypes.KVStoreService
	bank         types.BankKeeper
	earnings     types.EarningsKeeper
	cnft         types.CnftKeeper

	Schema        collections.Schema
	NextListingID collections.Item[uint64]
	NextBidID     collections.Item[uint64]
	Listings      collections.Map[uint64, types.Listing]
	Bids          collections.Map[collections.Pair[uint64, uint64], types.Bid]
	AssetListing  collections.Map[[]byte, uint64]
}

var _ types.CnftKeeper = cnftkeeper.Keeper{}

// NewKeeper builds an x/market keeper.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	bank types.BankKeeper,
	earnings types.EarningsKeeper,
	cnft types.CnftKeeper,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		storeService:  storeService,
		bank:          bank,
		earnings:      earnings,
		cnft:          cnft,
		NextListingID: collections.NewItem(sb, types.NextListingIDKey, "next_listing_id", collections.Uint64Value),
		NextBidID:     collections.NewItem(sb, types.NextBidIDKey, "next_bid_id", collections.Uint64Value),
		Listings:      collections.NewMap(sb, types.ListingsPrefix, "listings", collections.Uint64Key, codec.CollValue[types.Listing](cdc)),
		Bids: collections.NewMap(
			sb,
			types.BidsPrefix,
			"bids",
			collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key),
			codec.CollValue[types.Bid](cdc),
		),
		AssetListing: collections.NewMap(sb, types.AssetListingPrefix, "asset_listing", collections.BytesKey, collections.Uint64Value),
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

func (k Keeper) takeID(ctx context.Context, item collections.Item[uint64], name string) (uint64, error) {
	id, err := item.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load next %s id: %w", name, err)
	}
	if err := item.Set(ctx, id+1); err != nil {
		return 0, fmt.Errorf("failed to store next %s id: %w", name, err)
	}
	return id, nil
}

func (k Keeper) loadListing(ctx context.Context, id uint64) (types.Listing, error) {
	listing, err := k.Listings.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Listing{}, fmt.Errorf("listing %d does not exist", id)
		}
		return types.Listing{}, fmt.Errorf("failed to load listing %d: %w", id, err)
	}
	return listing, nil
}

func coin(amount math.Int) sdk.Coin {
	return sdk.NewCoin(params.BaseDenom, amount)
}
