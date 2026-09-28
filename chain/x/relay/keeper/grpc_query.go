package keeper

import (
	"context"
	"encoding/hex"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns an implementation of x/relay's QueryServer.
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

func (q queryServer) Reporters(goCtx context.Context, _ *types.QueryReportersRequest) (*types.QueryReportersResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	var reporters []string
	if err := q.Keeper.Reporters.Walk(ctx, nil, func(addr string, _ bool) (bool, error) {
		reporters = append(reporters, addr)
		return false, nil
	}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if reporters == nil {
		reporters = []string{}
	}
	return &types.QueryReportersResponse{Reporters: reporters}, nil
}

func (q queryServer) Relay(goCtx context.Context, req *types.QueryRelayRequest) (*types.QueryRelayResponse, error) {
	if req == nil || req.RsaFingerprintHex == "" {
		return nil, status.Error(codes.InvalidArgument, "rsa_fingerprint_hex is required")
	}
	fingerprint, err := hex.DecodeString(req.RsaFingerprintHex)
	if err != nil || len(fingerprint) != types.RSAFingerprintLen {
		return nil, status.Error(codes.InvalidArgument, "rsa_fingerprint_hex must be 20 bytes of hex")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	relay, found, err := q.Keeper.getRelay(ctx, fingerprint)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !found {
		return nil, status.Error(codes.NotFound, "relay not found")
	}
	return &types.QueryRelayResponse{Relay: relay}, nil
}

func (q queryServer) EpochResult(goCtx context.Context, req *types.QueryEpochResultRequest) (*types.QueryEpochResultResponse, error) {
	if req == nil || req.Epoch == 0 {
		return nil, status.Error(codes.InvalidArgument, "epoch is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	result, err := q.Keeper.EpochResults.Get(ctx, req.Epoch)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryEpochResultResponse{Result: result}, nil
}

func (q queryServer) Invariants(goCtx context.Context, _ *types.QueryInvariantsRequest) (*types.QueryInvariantsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	got, err := q.Keeper.CheckInvariants(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryInvariantsResponse{
		CeilingHolds: got.CeilingHolds,
		PayoutsMatch: got.PayoutsMatch,
		Detail:       got.Detail,
	}, nil
}
