package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns an implementation of x/fees's QueryServer interface.
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

func (q queryServer) BaseFee(goCtx context.Context, _ *types.QueryBaseFeeRequest) (*types.QueryBaseFeeResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	baseFee, err := q.Keeper.BaseFee.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryBaseFeeResponse{BaseFee: baseFee}, nil
}

func (q queryServer) Earnings(goCtx context.Context, req *types.QueryEarningsRequest) (*types.QueryEarningsResponse, error) {
	if req == nil || req.Address == "" {
		return nil, status.Error(codes.InvalidArgument, "address is required")
	}
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	balance, err := q.Keeper.GetEarnings(ctx, addr)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryEarningsResponse{Balance: balance}, nil
}

func (q queryServer) Deposit(goCtx context.Context, req *types.QueryDepositRequest) (*types.QueryDepositResponse, error) {
	if req == nil || req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	deposit, err := q.Keeper.GetDeposit(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryDepositResponse{Deposit: deposit}, nil
}
