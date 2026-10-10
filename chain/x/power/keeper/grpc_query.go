package keeper

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns an implementation of x/power's QueryServer interface.
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

func (q queryServer) BootstrapCommittee(goCtx context.Context, _ *types.QueryBootstrapCommitteeRequest) (*types.QueryBootstrapCommitteeResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	var members []types.BootstrapMember
	if err := q.Keeper.BootstrapCommittee.Walk(ctx, nil, func(_ string, m types.BootstrapMember) (bool, error) {
		members = append(members, m)
		return false, nil
	}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryBootstrapCommitteeResponse{Members: members}, nil
}

func (q queryServer) Lambda(goCtx context.Context, _ *types.QueryLambdaRequest) (*types.QueryLambdaResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	lambda, err := q.Keeper.Lambda.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	lastUpdated, err := q.Keeper.LambdaLastUpdatedEpoch.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	capBps, err := q.Keeper.CapCurrentBps.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	streak, err := q.Keeper.CapBelowStreak.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryLambdaResponse{
		Lambda:                  lambda,
		LastUpdatedEpoch:        lastUpdated,
		CurrentCapBps:           capBps,
		BelowStepUpStreakEpochs: streak,
	}, nil
}

func (q queryServer) ValidatorPower(goCtx context.Context, req *types.QueryValidatorPowerRequest) (*types.QueryValidatorPowerResponse, error) {
	if req == nil || req.OperatorAddress == "" {
		return nil, status.Error(codes.InvalidArgument, "operator_address is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)

	valAddr, err := sdk.ValAddressFromBech32(req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	valoperAddr := valAddr.String()

	power, err := q.Keeper.LastPower.Get(ctx, valoperAddr)
	if err != nil {
		if isNotFound(err) {
			power = 0
		} else {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	operator, err := q.Keeper.validatorOperator(ctx, valAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// BootstrapShare/CappedShare/PowerShare are not recomputed for a query (that requires the same
	// full block-universe computation EndBlock does, which needs an EmissionKeeper the query
	// server does not hold); only the last CometBFT power actually assigned is returned. A future
	// pass can wire an EmissionKeeper here if the fractional breakdown is needed over gRPC too.
	return &types.QueryValidatorPowerResponse{
		OperatorAddress: valoperAddr,
		BootstrapShare:  math.LegacyZeroDec(),
		CappedShare:     math.LegacyZeroDec(),
		PowerShare:      math.LegacyZeroDec(),
		CometPower:      power,
		Operator:        operator,
	}, nil
}

func (q queryServer) Invariants(goCtx context.Context, _ *types.QueryInvariantsRequest) (*types.QueryInvariantsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	detail, empty := q.Keeper.CheckInvariants(ctx)
	return &types.QueryInvariantsResponse{ModuleAccountEmpty: empty, Detail: detail}, nil
}
