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

// TreeFrontier answers the right edge of the note-commitment tree with the size and root it
// belongs to, read together so they describe one tree.
func (q queryServer) TreeFrontier(ctx context.Context, _ *types.QueryTreeFrontierRequest) (*types.QueryTreeFrontierResponse, error) {
	frontier, err := getBytes(ctx, q.Frontier)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	root, err := getBytes(ctx, q.CurrentRoot)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	size, err := getUint64(ctx, q.TreeSize)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryTreeFrontierResponse{
		Height: uint64(sdk.UnwrapSDKContext(ctx).BlockHeight()), TreeSize: size, CurrentRoot: root, Frontier: frontier,
	}, nil
}

// SubtreeRoots answers a page of the roots of the filled 2^16-leaf subtrees, in index order, with
// the number of filled subtrees. A start past the last one is an empty page, not an error: a wallet
// that is caught up asks for the next subtree and gets none.
func (q queryServer) SubtreeRoots(ctx context.Context, req *types.QuerySubtreeRootsRequest) (*types.QuerySubtreeRootsResponse, error) {
	limit := req.Limit
	if limit == 0 {
		limit = types.SubtreeRootsDefaultLimit
	}
	if limit > types.SubtreeRootsMaxLimit {
		return nil, status.Errorf(codes.InvalidArgument, "limit %d is over the most a page holds, %d", req.Limit, types.SubtreeRootsMaxLimit)
	}
	size, err := getUint64(ctx, q.TreeSize)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	total := size / types.SubtreeLeaves
	resp := &types.QuerySubtreeRootsResponse{Roots: []types.SubtreeRoot{}, Total: total}
	for index := req.StartIndex; index < total && uint32(len(resp.Roots)) < limit; index++ {
		root, err := q.Subtrees.Get(ctx, index)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "read the root of subtree %d, which the tree of %d leaves has filled: %v", index, size, err)
		}
		resp.Roots = append(resp.Roots, root)
	}
	return resp, nil
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
		AccumulatorMatches: inv.AccumulatorMatches, Detail: inv.Detail, SubtreeRootsMatch: inv.SubtreeRootsMatch,
	}, nil
}
