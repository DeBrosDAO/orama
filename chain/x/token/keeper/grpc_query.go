package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns an implementation of x/token's QueryServer interface.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return queryServer{Keeper: k}
}

func (q queryServer) Params(goCtx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	p, err := q.Keeper.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryParamsResponse{Params: p}, nil
}

func (q queryServer) Token(goCtx context.Context, req *types.QueryTokenRequest) (*types.QueryTokenResponse, error) {
	if req == nil || req.Denom == "" {
		return nil, status.Error(codes.InvalidArgument, "denom is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	token, err := q.Keeper.getToken(ctx, req.Denom)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryTokenResponse{Token: token}, nil
}

func (q queryServer) Frozen(goCtx context.Context, req *types.QueryFrozenRequest) (*types.QueryFrozenResponse, error) {
	if req == nil || req.Denom == "" || req.Account == "" {
		return nil, status.Error(codes.InvalidArgument, "denom and account are required")
	}
	account, err := sdk.AccAddressFromBech32(req.Account)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	if _, err := q.Keeper.getToken(ctx, req.Denom); err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	frozen, err := q.Keeper.isFrozen(ctx, req.Denom, account.String())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryFrozenResponse{Frozen: frozen}, nil
}

func (q queryServer) Invariants(goCtx context.Context, _ *types.QueryInvariantsRequest) (*types.QueryInvariantsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	got, err := q.Keeper.CheckInvariants(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryInvariantsResponse{
		SupplyMatches: got.SupplyMatches,
		DepositsMatch: got.DepositsMatch,
		Detail:        got.Detail,
	}, nil
}
