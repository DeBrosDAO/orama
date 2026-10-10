package keeper

import (
	"context"
	"cosmossdk.io/collections"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns x/houses' query server.
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

func (q queryServer) Proposal(goCtx context.Context, req *types.QueryProposalRequest) (*types.QueryProposalResponse, error) {
	if req == nil || req.ProposalId == 0 {
		return nil, status.Error(codes.InvalidArgument, "proposal_id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	p, err := q.Keeper.getProposal(ctx, req.ProposalId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryProposalResponse{Proposal: p}, nil
}

func (q queryServer) Vote(goCtx context.Context, req *types.QueryVoteRequest) (*types.QueryVoteResponse, error) {
	if req == nil || req.ProposalId == 0 || req.Voter == "" {
		return nil, status.Error(codes.InvalidArgument, "proposal_id and voter are required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	key := collections.Join(req.ProposalId, req.Voter)
	vote, err := q.Keeper.TokenVotes.Get(ctx, key)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if errors.Is(err, collections.ErrNotFound) {
		vote, err = q.Keeper.OperatorVotes.Get(ctx, key)
		if err != nil {
			return nil, status.Error(codes.NotFound, err.Error())
		}
	}
	return &types.QueryVoteResponse{Vote: vote}, nil
}

func (q queryServer) HouseBond(goCtx context.Context, req *types.QueryHouseBondRequest) (*types.QueryHouseBondResponse, error) {
	if req == nil || req.Address == "" {
		return nil, status.Error(codes.InvalidArgument, "address is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	bond, err := q.Keeper.Bonds.Get(ctx, req.Address)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryHouseBondResponse{Bond: bond}, nil
}

func (q queryServer) Tiers(goCtx context.Context, _ *types.QueryTiersRequest) (*types.QueryTiersResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	view, err := q.Keeper.Tiers(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryTiersResponse{
		ParameterOpen:     view.Parameter,
		StructuralOpen:    view.Structural,
		Lambda:            view.Lambda,
		BondedStake:       view.Bonded,
		EligibleOperators: uint64(view.Eligible),
		DistinctPrefix16:  uint64(view.Prefix16s),
		DistinctAsn:       uint64(view.ASNs),
	}, nil
}

func (q queryServer) Enacted(goCtx context.Context, _ *types.QueryEnactedRequest) (*types.QueryEnactedResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	enacted, err := q.Keeper.Enacted.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryEnactedResponse{Enacted: enacted}, nil
}

func (q queryServer) Invariants(goCtx context.Context, _ *types.QueryInvariantsRequest) (*types.QueryInvariantsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	got, err := q.Keeper.CheckInvariants(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryInvariantsResponse{BondsMatchModule: got.BondsMatchModule, Detail: got.Detail}, nil
}
