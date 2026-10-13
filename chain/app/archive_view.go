package app

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	storagekeeper "github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// archiveNodes is x/archive's view of x/nodes: an archiver is the hot key of
// an active node with an ARCHIVER role bond.
type archiveNodes struct {
	nodes nodeskeeper.Keeper
}

func (a archiveNodes) ArchiverOperator(ctx context.Context, nodeID, signer string) (string, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if nodeID == "" {
		return "", fmt.Errorf("node id is empty")
	}
	ok, err := a.nodes.IsRoleActive(sdkCtx, nodeID, nodestypes.RoleArchiver)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("node %s has no active ARCHIVER role", nodeID)
	}
	node, err := a.nodes.GetNode(sdkCtx, nodeID)
	if err != nil {
		return "", err
	}
	if node.HotKey != signer {
		return "", fmt.Errorf("%s is not the hot key of node %s", signer, nodeID)
	}
	return node.Operator, nil
}

// ArchiverActive reports whether nodeID is an active node with a bonded ARCHIVER role. A node that
// no longer exists is not active.
func (a archiveNodes) ArchiverActive(ctx context.Context, nodeID string) (bool, error) {
	ok, err := a.nodes.IsRoleActive(sdk.UnwrapSDKContext(ctx), nodeID, nodestypes.RoleArchiver)
	if errors.Is(err, nodestypes.ErrNotFound) {
		return false, nil
	}
	return ok, err
}

// archiveStorage is x/archive's view of x/storage deals.
type archiveStorage struct {
	storage storagekeeper.Keeper
}

func (a archiveStorage) ArchiveDealActive(ctx context.Context, dealID uint64) (bool, error) {
	return a.storage.ArchiveDealActive(sdk.UnwrapSDKContext(ctx), dealID)
}

func (a archiveStorage) ArchiveDealLive(ctx context.Context, dealID uint64) (bool, error) {
	return a.storage.ArchiveDealLive(sdk.UnwrapSDKContext(ctx), dealID)
}

func (a archiveStorage) CreateArchiveDeal(ctx context.Context, pieceRoot []byte, realLeafCount, paddedLeafCount, pieceBytes, durationEpochs uint64) (uint64, error) {
	return a.storage.CreateArchiveDeal(sdk.UnwrapSDKContext(ctx), storagetypes.PieceCommitment{
		Root:            pieceRoot,
		RealLeafCount:   realLeafCount,
		PaddedLeafCount: paddedLeafCount,
		PieceBytes:      pieceBytes,
	}, durationEpochs)
}
