package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// namedNode builds the answer for one claim: the node it identifies and the literal IPs among that
// node's endpoints, which are the addresses the name's A and AAAA records point at.
func (k Keeper) namedNode(ctx sdk.Context, claim types.NodeName) (types.NamedNode, error) {
	node, err := k.Nodes.Get(ctx, claim.NodeId)
	if err != nil {
		return types.NamedNode{}, err
	}
	return types.NamedNode{
		Name:     claim.Name,
		NodeId:   claim.NodeId,
		Operator: claim.Operator,
		Ips:      types.LiteralIPs(node.Endpoints),
	}, nil
}

func (q queryServer) NodeByName(goCtx context.Context, req *types.QueryNodeByNameRequest) (*types.QueryNodeByNameResponse, error) {
	if req == nil || req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	claim, err := q.Keeper.Names.Get(ctx, req.Name)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "no node holds the name %q", req.Name)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	named, err := q.Keeper.namedNode(ctx, claim)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryNodeByNameResponse{Node: named}, nil
}

func (q queryServer) NameOfNode(goCtx context.Context, req *types.QueryNameOfNodeRequest) (*types.QueryNameOfNodeResponse, error) {
	if req == nil || req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	name, err := q.Keeper.NodeNames.Get(ctx, req.NodeId)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "node %s holds no name", req.NodeId)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	claim, err := q.Keeper.Names.Get(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryNameOfNodeResponse{Name: claim}, nil
}

// NodeNames pages through every claimed name in name order. The DNS sync loop that serves the
// records pages through it: a page key resumes after the last name of the previous page.
func (q queryServer) NodeNames(goCtx context.Context, req *types.QueryNodeNamesRequest) (*types.QueryNodeNamesResponse, error) {
	if req == nil {
		req = &types.QueryNodeNamesRequest{}
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	nodes, page, err := query.CollectionPaginate(ctx, q.Keeper.Names, req.Pagination,
		func(_ string, claim types.NodeName) (types.NamedNode, error) {
			return q.Keeper.namedNode(ctx, claim)
		})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if nodes == nil {
		nodes = []types.NamedNode{}
	}
	return &types.QueryNodeNamesResponse{Nodes: nodes, Pagination: page}, nil
}
