package keeper

import (
	"encoding/hex"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func (k Keeper) requireOperator(ctx sdk.Context, addr string) error {
	has, err := k.Operators.Has(ctx, addr)
	if err != nil {
		return fmt.Errorf("load operator %s: %w", addr, err)
	}
	if !has {
		return fmt.Errorf("operator %s: %w", addr, types.ErrNotFound)
	}
	return nil
}

// GetOperator returns a registered operator.
func (k Keeper) GetOperator(ctx sdk.Context, addr string) (types.Operator, error) {
	canonical, err := types.CanonicalAddress(addr)
	if err != nil {
		return types.Operator{}, err
	}
	op, err := k.Operators.Get(ctx, canonical)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Operator{}, fmt.Errorf("operator %s: %w", canonical, types.ErrNotFound)
		}
		return types.Operator{}, fmt.Errorf("load operator %s: %w", canonical, err)
	}
	return op, nil
}

// GetNode returns a node by id.
func (k Keeper) GetNode(ctx sdk.Context, nodeID string) (types.Node, error) {
	node, err := k.Nodes.Get(ctx, nodeID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Node{}, fmt.Errorf("node %s: %w", nodeID, types.ErrNotFound)
		}
		return types.Node{}, fmt.Errorf("load node %s: %w", nodeID, err)
	}
	return node, nil
}

// GetCluster returns a cluster registry row.
func (k Keeper) GetCluster(ctx sdk.Context, clusterID string) (types.Cluster, error) {
	cluster, err := k.Clusters.Get(ctx, clusterID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Cluster{}, fmt.Errorf("cluster %s: %w", clusterID, types.ErrNotFound)
		}
		return types.Cluster{}, fmt.Errorf("load cluster %s: %w", clusterID, err)
	}
	return cluster, nil
}

func (k Keeper) requireNodeOwner(ctx sdk.Context, nodeID, signer string) (types.Node, string, error) {
	operator, err := types.CanonicalAddress(signer)
	if err != nil {
		return types.Node{}, "", err
	}
	node, err := k.GetNode(ctx, nodeID)
	if err != nil {
		return types.Node{}, "", err
	}
	if node.Operator != operator {
		return types.Node{}, "", fmt.Errorf("node %s: %w", nodeID, types.ErrUnauthorized)
	}
	return node, operator, nil
}

func closedNode(node types.Node) error {
	if node.Status == types.NodeStatusRetired || node.Status == types.NodeStatusTombstoned {
		return fmt.Errorf("node %s is %s", node.NodeId, node.Status)
	}
	return nil
}

func (k Keeper) saveNode(ctx sdk.Context, node types.Node) error {
	if err := k.resyncCapacity(ctx, &node); err != nil {
		return err
	}
	if err := k.Nodes.Set(ctx, node.NodeId, node); err != nil {
		return fmt.Errorf("save node %s: %w", node.NodeId, err)
	}
	return k.noteStorageChange(ctx, node)
}

func bondOf(node types.Node, role types.Role) math.Int {
	for _, bond := range node.Bonds {
		if bond.Role == role {
			if bond.Amount.IsNil() {
				return math.ZeroInt()
			}
			return bond.Amount
		}
	}
	return math.ZeroInt()
}

func setBond(node *types.Node, role types.Role, amount math.Int) {
	for i := range node.Bonds {
		if node.Bonds[i].Role == role {
			node.Bonds[i].Amount = amount
			return
		}
	}
	node.Bonds = append(node.Bonds, types.RoleBond{Role: role, Amount: amount})
}

func refreshStatus(node *types.Node, p types.Params) error {
	switch node.Status {
	case types.NodeStatusJailed, types.NodeStatusRetired, types.NodeStatusTombstoned:
		return nil
	}
	for _, role := range node.Roles {
		min, err := p.MinBondFor(role)
		if err != nil {
			return err
		}
		if !bondOf(*node, role).LT(min) {
			node.Status = types.NodeStatusActive
			return nil
		}
	}
	node.Status = types.NodeStatusRegistered
	return nil
}

func cloneBindings(in []types.Binding) []types.Binding {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.Binding, len(in))
	for i, binding := range in {
		out[i] = binding
		out[i].Pubkey = append([]byte(nil), binding.Pubkey...)
		out[i].Signature = append([]byte(nil), binding.Signature...)
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func pubkeyKey(pub []byte) string {
	return hex.EncodeToString(pub)
}

func (k Keeper) assertPubkeyAvailable(ctx sdk.Context, pub []byte, ownerNode string) error {
	key := pubkeyKey(pub)
	revoked, err := k.Revoked.Has(ctx, key)
	if err != nil {
		return fmt.Errorf("load revoked pubkey %s: %w", key, err)
	}
	if revoked {
		return fmt.Errorf("pubkey %s: %w", key, types.ErrPubkeyReused)
	}
	owner, err := k.LivePubkeys.Get(ctx, key)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("load live pubkey %s: %w", key, err)
	}
	if owner != ownerNode {
		return fmt.Errorf("pubkey %s is bound to node %s: %w", key, owner, types.ErrPubkeyReused)
	}
	return nil
}

func (k Keeper) verifyBindings(ctx sdk.Context, operator string, bindings []types.Binding, p types.Params) error {
	if len(bindings) == 0 {
		return fmt.Errorf("at least one binding is required")
	}
	if uint32(len(bindings)) > p.MaxBindings {
		return fmt.Errorf("%d bindings exceeds max %d", len(bindings), p.MaxBindings)
	}
	chainID := ctx.ChainID()
	if chainID == "" {
		return fmt.Errorf("chain id is empty")
	}
	for i, binding := range bindings {
		if err := types.VerifyBinding(chainID, operator, binding); err != nil {
			return fmt.Errorf("binding %d: %w", i, err)
		}
	}
	return nil
}

func (k Keeper) revokeBinding(ctx sdk.Context, binding types.Binding, nodeID string, reason types.Revocation) error {
	key := pubkeyKey(binding.Pubkey)
	if err := k.LivePubkeys.Remove(ctx, key); err != nil {
		return fmt.Errorf("remove live pubkey %s: %w", key, err)
	}
	record := types.RevokedPubkey{
		Pubkey:  append([]byte(nil), binding.Pubkey...),
		NodeId:  nodeID,
		Service: binding.Service,
		Reason:  reason,
	}
	if err := k.Revoked.Set(ctx, key, record); err != nil {
		return fmt.Errorf("revoke pubkey %s: %w", key, err)
	}
	return nil
}

func norama(amount math.Int) (sdk.Coins, error) {
	if err := types.PositiveAmount(amount); err != nil {
		return nil, err
	}
	return sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount)), nil
}

func (k Keeper) moduleBalance(ctx sdk.Context) math.Int {
	amount := k.bankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(types.ModuleName), params.BaseDenom).Amount
	if amount.IsNil() {
		return math.ZeroInt()
	}
	return amount
}

func nodeDepositID(nodeID string, part uint32) string {
	if part == 0 {
		return "nodes/node/" + nodeID
	}
	return fmt.Sprintf("nodes/node/%s/%d", nodeID, part)
}

func clusterDepositID(clusterID string, part uint32) string {
	if part == 0 {
		return "nodes/cluster/" + clusterID
	}
	return fmt.Sprintf("nodes/cluster/%s/%d", clusterID, part)
}

func measuredBytes(size int) (uint64, error) {
	if size < 0 {
		return 0, fmt.Errorf("negative proto size %d", size)
	}
	total := uint64(size) + types.DepositFieldOverhead
	if total < uint64(size) {
		return 0, fmt.Errorf("deposit size overflow")
	}
	return total, nil
}

func nodeChargeBytes(node types.Node) (uint64, error) {
	node.DepositBytes = 0
	node.DepositParts = 0
	return measuredBytes(node.Size())
}

func clusterChargeBytes(cluster types.Cluster) (uint64, error) {
	cluster.DepositBytes = 0
	cluster.DepositParts = 0
	return measuredBytes(cluster.Size())
}

func (k Keeper) lockSized(ctx sdk.Context, owner sdk.AccAddress, id string, bytes uint64) error {
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	const maxInt64 = uint64(^uint64(0) >> 1)
	if bytes > maxInt64 {
		return fmt.Errorf("deposit %s size %d exceeds int64", id, bytes)
	}
	amount := p.DepositPerByte.MulRaw(int64(bytes))
	if !amount.IsPositive() {
		return fmt.Errorf("deposit %s amount is not positive", id)
	}
	if err := k.depositKeeper.LockDeposit(ctx, owner, id, amount); err != nil {
		return fmt.Errorf("lock deposit %s: %w", id, err)
	}
	return nil
}

func (k Keeper) chargeGrowth(
	ctx sdk.Context,
	owner sdk.AccAddress,
	idFor func(uint32) string,
	haveBytes uint64,
	haveParts uint32,
	wantBytes uint64,
) (uint64, uint32, error) {
	if wantBytes == 0 {
		return 0, 0, fmt.Errorf("deposit size is zero")
	}
	if haveBytes == 0 {
		if err := k.lockSized(ctx, owner, idFor(0), wantBytes); err != nil {
			return 0, 0, err
		}
		return wantBytes, 0, nil
	}
	if wantBytes <= haveBytes {
		return haveBytes, haveParts, nil
	}
	next := haveParts + 1
	if next == 0 {
		return 0, 0, fmt.Errorf("deposit part counter overflow")
	}
	if err := k.lockSized(ctx, owner, idFor(next), wantBytes-haveBytes); err != nil {
		return 0, 0, err
	}
	return wantBytes, next, nil
}

func (k Keeper) releaseParts(ctx sdk.Context, idFor func(uint32) string, parts uint32, haveBytes uint64) error {
	if haveBytes == 0 {
		return nil
	}
	for part := uint32(0); part <= parts; part++ {
		id := idFor(part)
		if _, _, err := k.depositKeeper.ReleaseDeposit(ctx, id); err != nil {
			return fmt.Errorf("release deposit %s: %w", id, err)
		}
		if part == ^uint32(0) {
			break
		}
	}
	return nil
}

func (k Keeper) enqueueUnbonding(ctx sdk.Context, node types.Node, role types.Role, amount math.Int) error {
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	base := ctx.BlockTime().Unix()
	if base < 0 {
		return fmt.Errorf("block time is before the unix epoch")
	}
	completion := base + p.UnbondingSeconds
	if completion < base {
		return fmt.Errorf("unbonding completion overflows")
	}
	id, err := k.NextUnbonding.Next(ctx)
	if err != nil {
		return fmt.Errorf("allocate unbonding id: %w", err)
	}
	entry := types.UnbondingEntry{
		Id:             id,
		NodeId:         node.NodeId,
		Operator:       node.Operator,
		Role:           role,
		Amount:         amount,
		CompletionUnix: completion,
	}
	if err := k.Unbondings.Set(ctx, id, entry); err != nil {
		return fmt.Errorf("save unbonding %d: %w", id, err)
	}
	if err := k.UnbondingByTime.Set(ctx, collections.Join(completion, id), id); err != nil {
		return fmt.Errorf("index unbonding %d by time: %w", id, err)
	}
	if err := k.UnbondingByNode.Set(ctx, collections.Join(node.NodeId, id), id); err != nil {
		return fmt.Errorf("index unbonding %d by node: %w", id, err)
	}
	return nil
}

func (k Keeper) deleteUnbonding(ctx sdk.Context, entry types.UnbondingEntry) error {
	if err := k.Unbondings.Remove(ctx, entry.Id); err != nil {
		return fmt.Errorf("remove unbonding %d: %w", entry.Id, err)
	}
	if err := k.UnbondingByTime.Remove(ctx, collections.Join(entry.CompletionUnix, entry.Id)); err != nil {
		return fmt.Errorf("remove unbonding %d time index: %w", entry.Id, err)
	}
	if err := k.UnbondingByNode.Remove(ctx, collections.Join(entry.NodeId, entry.Id)); err != nil {
		return fmt.Errorf("remove unbonding %d node index: %w", entry.Id, err)
	}
	return nil
}

// NodeUnbondings returns the unbonding entries for a node, ordered by id.
func (k Keeper) NodeUnbondings(ctx sdk.Context, nodeID string) ([]types.UnbondingEntry, error) {
	return k.nodeUnbondingsUpTo(ctx, nodeID, 0)
}

// nodeUnbondingsUpTo returns at most max unbonding entries of a node (all of them when max is 0).
func (k Keeper) nodeUnbondingsUpTo(ctx sdk.Context, nodeID string, max int) ([]types.UnbondingEntry, error) {
	var out []types.UnbondingEntry
	err := k.UnbondingByNode.Walk(ctx, collections.NewPrefixedPairRange[string, uint64](nodeID), func(key collections.Pair[string, uint64], id uint64) (bool, error) {
		entry, err := k.Unbondings.Get(ctx, id)
		if err != nil {
			return true, fmt.Errorf("load unbonding %d: %w", id, err)
		}
		out = append(out, entry)
		return max > 0 && len(out) >= max, nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk unbondings for node %s: %w", nodeID, err)
	}
	return out, nil
}
