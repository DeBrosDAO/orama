package keeper

import (
	"context"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns an implementation of x/archive's QueryServer.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return queryServer{Keeper: k}
}

func (q queryServer) Params(goCtx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	params, err := q.Keeper.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryParamsResponse{Params: params}, nil
}

func (q queryServer) Range(goCtx context.Context, req *types.QueryRangeRequest) (*types.QueryRangeResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := types.ValidateHeights(req.StartHeight, req.EndHeight); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	rec, err := q.Keeper.GetRange(ctx, req.StartHeight, req.EndHeight)
	if err != nil {
		if errors.Is(err, types.ErrUnknownRange) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryRangeResponse{Range: rec}, nil
}

func (q queryServer) LastArchivedHeight(goCtx context.Context, _ *types.QueryLastArchivedHeightRequest) (*types.QueryLastArchivedHeightResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	last, err := q.Keeper.LastArchivedHeight.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryLastArchivedHeightResponse{LastArchivedHeight: last}, nil
}

func (q queryServer) RetainHeight(goCtx context.Context, _ *types.QueryRetainHeightRequest) (*types.QueryRetainHeightResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	retain, err := q.Keeper.RetainHeight(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	params, err := q.Keeper.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	last, err := q.Keeper.LastArchivedHeight.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryRetainHeightResponse{
		RetainHeight:          retain,
		Tip:                   ctx.BlockHeight(),
		LastArchivedHeight:    last,
		RetentionWindowBlocks: params.RetentionWindowBlocks,
	}, nil
}
