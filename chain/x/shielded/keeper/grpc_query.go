package keeper

import (
	"context"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

type queryServer struct{ Keeper }

// NewQueryServerImpl returns the module's gRPC query server.
func NewQueryServerImpl(k Keeper) types.QueryServer { return queryServer{k} }

func (q queryServer) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	p, err := q.params(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryParamsResponse{Params: p}, nil
}

func (q queryServer) Pools(ctx context.Context, _ *types.QueryPoolsRequest) (*types.QueryPoolsResponse, error) {
	var pools []types.PoolBalance
	err := q.Keeper.Pools.Walk(ctx, nil, func(key collections.Pair[uint32, []byte], bal math.Int) (bool, error) {
		pools = append(pools, types.PoolBalance{Vintage: key.K1(), Asset: key.K2(), Balance: bal})
		return false, nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	queue, err := q.queued(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryPoolsResponse{Pools: pools, Queue: queue}, nil
}

func (q queryServer) TreeState(ctx context.Context, _ *types.QueryTreeStateRequest) (*types.QueryTreeStateResponse, error) {
	root, err := getBytes(ctx, q.CurrentRoot)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	size, err := getUint64(ctx, q.TreeSize)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	count, err := getUint64(ctx, q.NullifierCount)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	acc, err := q.accumulator(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	var anchors uint64
	if err := q.Anchors.Walk(ctx, nil, func([]byte, int64) (bool, error) { anchors++; return false, nil }); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryTreeStateResponse{
		TreeSize: size, CurrentRoot: root, Anchors: anchors, NullifierAccumulator: acc[:], NullifierCount: count,
	}, nil
}

func (q queryServer) NullifierSpent(ctx context.Context, req *types.QueryNullifierSpentRequest) (*types.QueryNullifierSpentResponse, error) {
	var nf [bundle.NodeLen]byte
	if copy(nf[:], req.Nullifier) != bundle.NodeLen || len(req.Nullifier) != bundle.NodeLen {
		return nil, status.Error(codes.InvalidArgument, "nullifier must be 32 bytes")
	}
	spent, err := q.deps.Nullifiers.Spent(nf, sdk.UnwrapSDKContext(ctx).BlockHeight()+1)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryNullifierSpentResponse{Spent: spent}, nil
}

func (q queryServer) Invariants(ctx context.Context, _ *types.QueryInvariantsRequest) (*types.QueryInvariantsResponse, error) {
	inv, err := q.CheckInvariants(sdk.UnwrapSDKContext(ctx))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryInvariantsResponse{
		BalanceMatches: inv.BalanceMatches, PoolsNonNegative: inv.PoolsNonNegative,
		AccumulatorMatches: inv.AccumulatorMatches, Detail: inv.Detail,
	}, nil
}
