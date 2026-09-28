package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns an implementation of x/nodes' QueryServer.
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

func (q queryServer) Operator(goCtx context.Context, req *types.QueryOperatorRequest) (*types.QueryOperatorResponse, error) {
	if req == nil || req.Address == "" {
		return nil, status.Error(codes.InvalidArgument, "address is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	op, err := q.Keeper.GetOperator(ctx, req.Address)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryOperatorResponse{Operator: op}, nil
}

func (q queryServer) Node(goCtx context.Context, req *types.QueryNodeRequest) (*types.QueryNodeResponse, error) {
	if req == nil || req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	node, err := q.Keeper.GetNode(ctx, req.NodeId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryNodeResponse{Node: node}, nil
}

func (q queryServer) Cluster(goCtx context.Context, req *types.QueryClusterRequest) (*types.QueryClusterResponse, error) {
	if req == nil || req.ClusterId == "" {
		return nil, status.Error(codes.InvalidArgument, "cluster_id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	cluster, err := q.Keeper.GetCluster(ctx, req.ClusterId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryClusterResponse{Cluster: cluster}, nil
}

func (q queryServer) NodeUnbondings(goCtx context.Context, req *types.QueryNodeUnbondingsRequest) (*types.QueryNodeUnbondingsResponse, error) {
	if req == nil || req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	entries, err := q.Keeper.NodeUnbondings(ctx, req.NodeId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if entries == nil {
		entries = []types.UnbondingEntry{}
	}
	return &types.QueryNodeUnbondingsResponse{Unbondings: entries}, nil
}

func (q queryServer) Invariants(goCtx context.Context, _ *types.QueryInvariantsRequest) (*types.QueryInvariantsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	got, err := q.Keeper.CheckInvariants(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryInvariantsResponse{
		BalanceMatches:    got.BalanceMatches,
		ActiveRolesBonded: got.ActiveRolesBonded,
		CapacityBacked:    got.CapacityBacked,
		Detail:            got.Detail,
	}, nil
}
