package keeper

import (
	"context"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/market/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns x/market's query server.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return queryServer{Keeper: k}
}

func (q queryServer) Listing(goCtx context.Context, req *types.QueryListingRequest) (*types.QueryListingResponse, error) {
	if req == nil || req.Id == 0 {
		return nil, status.Error(codes.InvalidArgument, "listing id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	listing, err := q.loadListing(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryListingResponse{Listing: listing}, nil
}

func (q queryServer) Bid(goCtx context.Context, req *types.QueryBidRequest) (*types.QueryBidResponse, error) {
	if req == nil || req.ListingId == 0 || req.BidId == 0 {
		return nil, status.Error(codes.InvalidArgument, "listing id and bid id are required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	bid, err := q.Bids.Get(ctx, collections.Join(req.ListingId, req.BidId))
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryBidResponse{Bid: bid}, nil
}
