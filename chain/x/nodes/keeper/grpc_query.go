package keeper

import (
	"context"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/internal/pagebound"
	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

var _ types.QueryServer = queryServer{}

// MaxUnbondingsPerQuery bounds one NodeUnbondings response: an operator can queue many small
// unbondings on a node, and a public query must not walk them all.
const MaxUnbondingsPerQuery = 1000

// MaxNodesPerPage bounds one Nodes page by entries, and MaxNodesPageBytes by the size of the
// records it reads. A node record carries its bindings and endpoints, up to 64 of each at the
// largest genesis parameters (about 26 KB), so 100 such records read more than the node's query
// gas limit allows; a page that reaches MaxNodesPageBytes ends there and its next_key resumes at
// the next node. 400 KB reads cost about 1.3 million gas (3 per byte read, plus the flat cost per
// read), under the 2,000,000 query-gas-limit the installer writes, so a page of the typical
// record (a few endpoints and bindings) is the full 100 and one of the largest records stops at 15.
const (
	MaxNodesPerPage   = 100
	MaxNodesPageBytes = 400 * 1024
)

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
	entries, err := q.Keeper.nodeUnbondingsUpTo(ctx, req.NodeId, MaxUnbondingsPerQuery)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if entries == nil {
		entries = []types.UnbondingEntry{}
	}
	return &types.QueryNodeUnbondingsResponse{Unbondings: entries}, nil
}

// Nodes pages through every registered node in node id order, whatever its status. A page key
// resumes after the last node of the previous page. It runs on the caller's gas meter: a page is
// bounded by entries and by the bytes it reads (MaxNodesPageBytes).
func (q queryServer) Nodes(goCtx context.Context, req *types.QueryNodesRequest) (*types.QueryNodesResponse, error) {
	if req == nil {
		req = &types.QueryNodesRequest{}
	}
	pagination, err := pagebound.Clamp(req.Pagination, MaxNodesPerPage)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	nodes, next, err := q.nodesPage(sdk.UnwrapSDKContext(goCtx), pagination)
	if err != nil {
		return nil, err
	}
	return &types.QueryNodesResponse{Nodes: nodes, Pagination: &query.PageResponse{NextKey: next}}, nil
}

// nodesPage reads one page from the page key on, and returns the key of the node the next page
// starts at, nil after the last node. The key has the form query.CollectionPaginate gives.
func (q queryServer) nodesPage(ctx sdk.Context, page *query.PageRequest) ([]types.Node, []byte, error) {
	if page.Reverse {
		return nil, nil, status.Error(codes.InvalidArgument, "the node list is read in node id order only")
	}
	codec := q.Keeper.Nodes.KeyCodec()
	var rng collections.Ranger[string]
	if len(page.Key) > 0 {
		_, start, err := codec.Decode(page.Key)
		if err != nil {
			return nil, nil, status.Errorf(codes.InvalidArgument, "pagination.key is not a node id key: %v", err)
		}
		rng = new(collections.Range[string]).StartInclusive(start)
	}
	limit := page.Limit
	if limit == 0 {
		limit = MaxNodesPerPage
	}
	iter, err := q.Keeper.Nodes.Iterate(ctx, rng)
	if err != nil {
		return nil, nil, status.Errorf(codes.Internal, "failed to iterate the nodes: %v", err)
	}
	defer iter.Close()
	nodes, read := []types.Node{}, 0
	for ; iter.Valid(); iter.Next() {
		kv, err := iter.KeyValue()
		if err != nil {
			return nil, nil, status.Errorf(codes.Internal, "failed to read a node: %v", err)
		}
		if uint64(len(nodes)) == limit || (len(nodes) > 0 && read+kv.Value.Size() > MaxNodesPageBytes) {
			next := make([]byte, codec.Size(kv.Key))
			if _, err := codec.Encode(next, kv.Key); err != nil {
				return nil, nil, status.Errorf(codes.Internal, "failed to encode the next page key: %v", err)
			}
			return nodes, next, nil
		}
		nodes, read = append(nodes, kv.Value), read+kv.Value.Size()
	}
	return nodes, nil, nil
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
