package keeper

import (
	"context"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
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
	if req == nil || req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required: an epoch's challenges across every node are not served in one call")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	// A challenge key is (epoch, "node|deal|slot"). '|' is 0x7c and '}' is 0x7d, so this range is
	// exactly the keys of req.NodeId and the walk never leaves that node.
	rng := new(collections.Range[collections.Pair[uint64, string]]).
		StartInclusive(collections.Join(req.Epoch, req.NodeId+"|")).
		EndExclusive(collections.Join(req.Epoch, req.NodeId+"}"))
	var out []types.Challenge
	err := q.Keeper.Challenges.Walk(ctx, rng, func(key collections.Pair[uint64, string], rec types.ChallengeRecord) (bool, error) {
		dealID, slot, _, err := parseChallengeID(key.K2())
		if err != nil {
			return false, err
		}
		out = append(out, types.Challenge{DealId: dealID, Slot: slot, LeafIndex: rec.LeafIndex, Proved: rec.Proved})
		return len(out) >= maxChallengesPerQuery, nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryChallengesResponse{Challenges: out}, nil
}

// maxChallengesPerQuery bounds one Challenges response. A node is challenged on k_c sampled
// replicas plus its re-challenge set, which stays far below this.
const maxChallengesPerQuery = 1000

// NodeFailures returns the consecutive-failure counts of one node's rolled-back work.
func (q queryServer) NodeFailures(goCtx context.Context, req *types.QueryNodeFailuresRequest) (*types.QueryNodeFailuresResponse, error) {
	if req == nil || req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	var out []types.FailureCount
	err := q.Keeper.Failures.Walk(ctx, collections.NewPrefixedPairRange[string, string](req.NodeId), func(key collections.Pair[string, string], n uint64) (bool, error) {
		out = append(out, types.FailureCount{NodeId: key.K1(), Kind: key.K2(), Consecutive: n})
		return false, nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryNodeFailuresResponse{Failures: out}, nil
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

// Invariants walks every deal and its slots to sum escrow, so its cost grows with the chain's
// history, not with a request. The node's query-gas-limit (app.toml) bounds the public point
// lookups; this audit query is withheld from the public route (core chainread withheldQuery), so
// it runs on a meter of its own. Under the limit it ran out of gas once the deal count passed a
// couple of hundred, and the e2e invariant check reported query_failed.
func (q queryServer) Invariants(goCtx context.Context, _ *types.QueryInvariantsRequest) (*types.QueryInvariantsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx).WithGasMeter(storetypes.NewInfiniteGasMeter())
	got, err := q.Keeper.CheckInvariants(ctx)
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
