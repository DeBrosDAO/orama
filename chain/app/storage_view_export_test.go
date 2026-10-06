package app

import (
	"context"

	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
)

// StorageNodesJailForTest exposes the jail of x/storage's view of x/nodes to the black-box tests.
func StorageNodesJailForTest(k nodeskeeper.Keeper) func(ctx context.Context, nodeID string) error {
	return storageNodes{nodes: k}.Jail
}
