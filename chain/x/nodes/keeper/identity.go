package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// assertHotKeyAvailable refuses a hot key that is the operator, any operator, or the hot key of
// another live node. Possession of the key is proved separately, by the node's "hot-key" binding.
func (k Keeper) assertHotKeyAvailable(ctx sdk.Context, hot, operator, ownerNode string) error {
	if hot == operator {
		return fmt.Errorf("node %s: %w", ownerNode, types.ErrHotKey)
	}
	isOperator, err := k.Operators.Has(ctx, hot)
	if err != nil {
		return fmt.Errorf("load operator %s: %w", hot, err)
	}
	if isOperator {
		return fmt.Errorf("hot key %s is an operator: %w", hot, types.ErrHotKey)
	}
	owner, err := k.HotKeys.Get(ctx, hot)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("load hot key %s: %w", hot, err)
	}
	if owner != ownerNode {
		return fmt.Errorf("hot key %s is already the hot key of node %s: %w", hot, owner, types.ErrHotKey)
	}
	return nil
}

// assertIPsAvailable refuses a literal-IP endpoint that another live node has registered. The chain
// cannot prove control of an address, but it can make each address one node's.
func (k Keeper) assertIPsAvailable(ctx sdk.Context, ips []string, ownerNode string) error {
	for _, ip := range ips {
		owner, err := k.LiveIPs.Get(ctx, ip)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				continue
			}
			return fmt.Errorf("load endpoint address %s: %w", ip, err)
		}
		if owner != ownerNode {
			return fmt.Errorf("endpoint address %s is registered by node %s: %w", ip, owner, types.ErrEndpointTaken)
		}
	}
	return nil
}

// indexHotKey records hot as nodeID's hot key, replacing oldHot (empty when there was none).
func (k Keeper) indexHotKey(ctx sdk.Context, oldHot, hot, nodeID string) error {
	if oldHot != "" && oldHot != hot {
		if err := k.HotKeys.Remove(ctx, oldHot); err != nil {
			return fmt.Errorf("unindex hot key %s: %w", oldHot, err)
		}
	}
	if err := k.HotKeys.Set(ctx, hot, nodeID); err != nil {
		return fmt.Errorf("index hot key %s: %w", hot, err)
	}
	return nil
}

// indexIPs makes the literal IPs in next the node's, and releases those in prev that next drops.
func (k Keeper) indexIPs(ctx sdk.Context, prev, next []string, nodeID string) error {
	keep := make(map[string]struct{}, len(next))
	for _, ip := range next {
		keep[ip] = struct{}{}
	}
	for _, ip := range types.LiteralIPs(prev) {
		if _, ok := keep[ip]; ok {
			continue
		}
		if err := k.LiveIPs.Remove(ctx, ip); err != nil {
			return fmt.Errorf("release endpoint address %s: %w", ip, err)
		}
	}
	for ip := range keep {
		if err := k.LiveIPs.Set(ctx, ip, nodeID); err != nil {
			return fmt.Errorf("index endpoint address %s: %w", ip, err)
		}
	}
	return nil
}

// releaseIdentity drops a closing node's hot key and endpoint addresses from the indexes.
func (k Keeper) releaseIdentity(ctx sdk.Context, node types.Node) error {
	if err := k.HotKeys.Remove(ctx, node.HotKey); err != nil {
		return fmt.Errorf("unindex hot key %s: %w", node.HotKey, err)
	}
	return k.indexIPs(ctx, node.Endpoints, nil, node.NodeId)
}

// effectiveIdentity returns a node's network group and ASN when the pair has stood for the
// network_identity_lock_seconds, and ("", 0) while it is still inside the lock: a new node, or one
// that just changed either value, is treated as unidentified for protocol deals and the operator
// house. The lock is a rolling snapshot: only an identity that was already in place a lock ago
// counts, so identity cannot be moved to fit a slot draw or a house vote.
func (k Keeper) effectiveIdentity(ctx sdk.Context, node types.Node) (string, uint32, error) {
	p, err := k.params(ctx)
	if err != nil {
		return "", 0, err
	}
	if !types.IdentityEffective(node.IdentitySinceUnix, ctx.BlockTime().Unix(), p.NetworkIdentityLockSeconds) {
		return "", 0, nil
	}
	return types.NetworkOf(node.Endpoints), node.Asn, nil
}

// EffectiveNetwork is effectiveIdentity for callers outside the module (the operator house view).
func (k Keeper) EffectiveNetwork(ctx sdk.Context, node types.Node) (string, uint32, error) {
	return k.effectiveIdentity(ctx, node)
}
