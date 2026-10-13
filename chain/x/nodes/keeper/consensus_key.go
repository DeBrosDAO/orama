package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// OperatorOfConsensusKey returns the operator of the live node whose consensus binding holds
// pubkey, and false when no live node binds that key under the consensus service. A retired or
// tombstoned node has released its keys, so it names no operator. It implements
// x/power's types.OperatorResolver.
func (k Keeper) OperatorOfConsensusKey(ctx context.Context, pubkey []byte) (string, bool, error) {
	key := pubkeyKey(pubkey)
	nodeID, err := k.LivePubkeys.Get(ctx, key)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to load live pubkey %s: %w", key, err)
	}
	node, err := k.Nodes.Get(ctx, nodeID)
	if err != nil {
		return "", false, fmt.Errorf("failed to load node %s of live pubkey %s: %w", nodeID, key, err)
	}
	for _, binding := range node.Bindings {
		if binding.Service == types.ConsensusService && pubkeyKey(binding.Pubkey) == key {
			return node.Operator, true, nil
		}
	}
	return "", false, nil
}
