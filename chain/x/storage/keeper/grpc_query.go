package keeper

import (
	"context"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServer returns x/storage's query server.
func NewQueryServer(k Keeper) types.QueryServer {
	return queryServer{Keeper: k}
}

func (q queryServer) Params(goCtx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	p, err := q.Keeper.Params.Get(sdk.UnwrapSDKContext(goCtx))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryParamsResponse{Params: p}, nil
}

func (q queryServer) Deal(goCtx context.Context, req *types.QueryDealRequest) (*types.QueryDealResponse, error) {
	if req == nil || req.DealId == 0 {
		return nil, status.Error(codes.InvalidArgument, "deal_id is required")
	}
	deal, err := q.Keeper.loadDeal(sdk.UnwrapSDKContext(goCtx), req.DealId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryDealResponse{Deal: deal}, nil
}

func (q queryServer) Slot(goCtx context.Context, req *types.QuerySlotRequest) (*types.QuerySlotResponse, error) {
	if req == nil || req.DealId == 0 {
		return nil, status.Error(codes.InvalidArgument, "deal_id is required")
	}
	slot, err := q.Keeper.loadSlot(sdk.UnwrapSDKContext(goCtx), req.DealId, req.Slot)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QuerySlotResponse{Slot: slot}, nil
}

func (q queryServer) Authorization(goCtx context.Context, req *types.QueryAuthorizationRequest) (*types.QueryAuthorizationResponse, error) {
	if req == nil || req.Granter == "" || req.Grantee == "" {
		return nil, status.Error(codes.InvalidArgument, "granter and grantee are required")
	}
	auth, err := q.Keeper.Auths.Get(sdk.UnwrapSDKContext(goCtx), collections.Join(req.Granter, req.Grantee))
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryAuthorizationResponse{Authorization: auth}, nil
}

func (q queryServer) Challenges(goCtx context.Context, req *types.QueryChallengesRequest) (*types.QueryChallengesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	var out []types.Challenge
	err := q.Keeper.Challenges.Walk(ctx, collections.NewPrefixedPairRange[uint64, string](req.Epoch), func(key collections.Pair[uint64, string], rec types.ChallengeRecord) (bool, error) {
		dealID, slot, nodeID, err := parseChallengeID(key.K2())
		if err != nil {
			return false, err
		}
		if req.NodeId != "" && nodeID != req.NodeId {
			return false, nil
		}
		out = append(out, types.Challenge{DealId: dealID, Slot: slot, LeafIndex: rec.LeafIndex, Proved: rec.Proved})
		return false, nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryChallengesResponse{Challenges: out}, nil
}

func (q queryServer) EpochMint(goCtx context.Context, req *types.QueryEpochMintRequest) (*types.QueryEpochMintResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	minted, err := q.Keeper.epochMinted(ctx, req.Epoch)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	ceiling, err := q.Keeper.emission.StorageCeiling(ctx, req.Epoch)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if ceiling.IsNil() {
		ceiling = math.ZeroInt()
	}
	return &types.QueryEpochMintResponse{Minted: minted, Ceiling: ceiling}, nil
}

func (q queryServer) Queue(goCtx context.Context, _ *types.QueryQueueRequest) (*types.QueryQueueResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	head, err := q.Keeper.QueueHead.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	tail, err := q.Keeper.QueueTail.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryQueueResponse{Pending: tail - head, Head: head, Tail: tail}, nil
}

func (q queryServer) Invariants(goCtx context.Context, _ *types.QueryInvariantsRequest) (*types.QueryInvariantsResponse, error) {
	got, err := q.Keeper.CheckInvariants(sdk.UnwrapSDKContext(goCtx))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryInvariantsResponse{
		EscrowConserved:        got.EscrowConserved,
		SubsidyWithinCeiling:   got.SubsidyWithinCeiling,
		DistinctOperators:      got.DistinctOperators,
		ReservedWithinDeclared: got.ReservedWithinDeclared,
		QueueWellFormed:        got.QueueWellFormed,
		Detail:                 got.Detail,
	}, nil
}
