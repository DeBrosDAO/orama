package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns an implementation of x/emission's QueryServer interface.
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

func (q queryServer) CurrentEpoch(goCtx context.Context, _ *types.QueryCurrentEpochRequest) (*types.QueryCurrentEpochResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	state, err := q.Keeper.EpochState.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryCurrentEpochResponse{EpochState: state}, nil
}

func (q queryServer) ScheduleAt(_ context.Context, req *types.QueryScheduleAtRequest) (*types.QueryScheduleAtResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	if req.Epoch > types.MaxSafeEpoch {
		return nil, status.Errorf(codes.InvalidArgument, "epoch must be at most %d", uint64(types.MaxSafeEpoch))
	}
	maxMint := types.MaxMintableForEpoch(req.Epoch)
	split := types.SplitEpochMint(maxMint)
	return &types.QueryScheduleAtResponse{
		Epoch:              req.Epoch,
		MaxMintable:        maxMint,
		ValidatorShare:     split.Validator,
		StorageCeiling:     split.Storage,
		RelayCeiling:       split.Relay,
		DevelopmentCeiling: split.Development,
	}, nil
}

func (q queryServer) CumulativeMinted(goCtx context.Context, _ *types.QueryCumulativeMintedRequest) (*types.QueryCumulativeMintedResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	state, err := q.Keeper.EpochState.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryCumulativeMintedResponse{
		CumulativeMinted: state.CumulativeMinted,
		CumulativeBurned: state.CumulativeBurned,
	}, nil
}

func (q queryServer) SupplyCapSoFar(_ context.Context, req *types.QuerySupplyCapSoFarRequest) (*types.QuerySupplyCapSoFarResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	if req.Epoch > types.MaxSafeEpoch {
		return nil, status.Errorf(codes.InvalidArgument, "epoch must be at most %d", uint64(types.MaxSafeEpoch))
	}
	return &types.QuerySupplyCapSoFarResponse{
		SupplyCap: types.CumulativeScheduleMax(req.Epoch),
	}, nil
}

func (q queryServer) Invariants(goCtx context.Context, _ *types.QueryInvariantsRequest) (*types.QueryInvariantsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	detail, mintedWithinSchedule, supplyMatches := q.Keeper.checkSupplyInvariantDetailed(ctx)
	return &types.QueryInvariantsResponse{
		MintedWithinSchedule: mintedWithinSchedule,
		SupplyMatchesMinted:  supplyMatches,
		Detail:               detail,
	}, nil
}
