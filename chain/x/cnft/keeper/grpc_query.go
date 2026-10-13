package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

var _ types.QueryServer = queryServer{}

// MaxSnapshotsPerQuery bounds one Snapshots response so a public query never walks a whole tree's
// history: the first MaxSnapshotsPerQuery snapshots, oldest first.
const MaxSnapshotsPerQuery = 1000

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns x/cnft's query server.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return queryServer{Keeper: k}
}

func (q queryServer) Collection(goCtx context.Context, req *types.QueryCollectionRequest) (*types.QueryCollectionResponse, error) {
	if req == nil || req.Id == 0 {
		return nil, status.Error(codes.InvalidArgument, "collection id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	col, err := q.GetCollection(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryCollectionResponse{Collection: col}, nil
}

func (q queryServer) Tree(goCtx context.Context, req *types.QueryTreeRequest) (*types.QueryTreeResponse, error) {
	if req == nil || req.Id == 0 {
		return nil, status.Error(codes.InvalidArgument, "tree id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	tree, err := q.GetTree(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryTreeResponse{Tree: tree}, nil
}

func (q queryServer) Decompressed(goCtx context.Context, req *types.QueryDecompressedRequest) (*types.QueryDecompressedResponse, error) {
	if req == nil || len(req.AssetId) != types.HashSize {
		return nil, status.Error(codes.InvalidArgument, "asset_id must be 32 bytes")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	asset, err := q.Keeper.Decompressed.Get(ctx, req.AssetId)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "asset is not decompressed")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryDecompressedResponse{Asset: asset}, nil
}

func (q queryServer) Snapshots(goCtx context.Context, req *types.QuerySnapshotsRequest) (*types.QuerySnapshotsResponse, error) {
	if req == nil || req.TreeId == 0 {
		return nil, status.Error(codes.InvalidArgument, "tree id is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	var snaps []types.Snapshot
	rng := collections.NewPrefixedPairRange[uint64, uint64](req.TreeId)
	if err := q.Keeper.Snapshots.Walk(ctx, rng, func(_ collections.Pair[uint64, uint64], snap types.Snapshot) (bool, error) {
		snaps = append(snaps, snap)
		return len(snaps) >= MaxSnapshotsPerQuery, nil
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Errorf("failed to walk snapshots: %w", err).Error())
	}
	if snaps == nil {
		snaps = []types.Snapshot{}
	}
	return &types.QuerySnapshotsResponse{Snapshots: snaps}, nil
}
