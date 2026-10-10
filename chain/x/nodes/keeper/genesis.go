package keeper

import (
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// InitGenesis writes genesis state. The nodes module account must already
// hold bonds + unbonding + name deposit norama; this method does not mint.
func (k Keeper) InitGenesis(ctx sdk.Context, gs types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return fmt.Errorf("invalid nodes genesis state: %w", err)
	}
	chainID := ctx.ChainID()
	if chainID == "" {
		return fmt.Errorf("chain id is empty")
	}
	for _, node := range gs.Nodes {
		if node.Status == types.NodeStatusRetired || node.Status == types.NodeStatusTombstoned {
			continue
		}
		for _, binding := range node.Bindings {
			if err := types.VerifyBinding(chainID, node.Operator, binding); err != nil {
				return fmt.Errorf("node %s: %w", node.NodeId, err)
			}
		}
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return fmt.Errorf("set nodes params: %w", err)
	}
	for _, op := range gs.Operators {
		if err := k.Operators.Set(ctx, op.Address, op); err != nil {
			return fmt.Errorf("set operator %s: %w", op.Address, err)
		}
	}
	for _, rev := range gs.RevokedPubkeys {
		if err := k.Revoked.Set(ctx, pubkeyKey(rev.Pubkey), rev); err != nil {
			return fmt.Errorf("set revoked pubkey: %w", err)
		}
	}
	for _, node := range gs.Nodes {
		for _, binding := range node.Bindings {
			if err := k.LivePubkeys.Set(ctx, pubkeyKey(binding.Pubkey), node.NodeId); err != nil {
				return fmt.Errorf("index pubkey for node %s: %w", node.NodeId, err)
			}
		}
		if node.Status != types.NodeStatusRetired && node.Status != types.NodeStatusTombstoned {
			if err := k.indexHotKey(ctx, "", node.HotKey, node.NodeId); err != nil {
				return err
			}
			if err := k.indexIPs(ctx, nil, types.LiteralIPs(node.Endpoints), node.NodeId); err != nil {
				return err
			}
		}
		if err := k.saveNode(ctx, node); err != nil {
			return err
		}
	}
	for _, cluster := range gs.Clusters {
		if err := k.Clusters.Set(ctx, cluster.ClusterId, cluster); err != nil {
			return fmt.Errorf("set cluster %s: %w", cluster.ClusterId, err)
		}
	}
	for _, entry := range gs.Unbondings {
		if err := k.Unbondings.Set(ctx, entry.Id, entry); err != nil {
			return fmt.Errorf("set unbonding %d: %w", entry.Id, err)
		}
		if err := k.UnbondingByTime.Set(ctx, collections.Join(entry.CompletionUnix, entry.Id), entry.Id); err != nil {
			return fmt.Errorf("index unbonding %d by time: %w", entry.Id, err)
		}
		if err := k.UnbondingByNode.Set(ctx, collections.Join(entry.NodeId, entry.Id), entry.Id); err != nil {
			return fmt.Errorf("index unbonding %d by node: %w", entry.Id, err)
		}
	}
	if gs.NextUnbondingId > 0 {
		if err := k.NextUnbonding.Set(ctx, gs.NextUnbondingId); err != nil {
			return fmt.Errorf("set unbonding sequence: %w", err)
		}
	}
	for _, claim := range gs.NodeNames {
		if err := k.indexName(ctx, claim); err != nil {
			return err
		}
	}
	for _, day := range gs.ServiceDays {
		if err := k.ServiceDays.Set(ctx, collections.Join(day.Operator, day.DayIndex), day); err != nil {
			return fmt.Errorf("set service day %s/%d: %w", day.Operator, day.DayIndex, err)
		}
	}
	got, err := k.CheckInvariants(ctx)
	if err != nil {
		return err
	}
	if !got.BalanceMatches || !got.ActiveRolesBonded || !got.CapacityBacked {
		return fmt.Errorf("genesis breaks x/nodes invariants:\n%s", got.Detail)
	}
	return nil
}

// ExportGenesis reads x/nodes state back into a GenesisState.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	p, err := k.params(ctx)
	if err != nil {
		return nil, err
	}
	gs := &types.GenesisState{Params: p}
	if err := k.Operators.Walk(ctx, nil, func(_ string, op types.Operator) (bool, error) {
		gs.Operators = append(gs.Operators, op)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("export operators: %w", err)
	}
	if err := k.Nodes.Walk(ctx, nil, func(_ string, node types.Node) (bool, error) {
		gs.Nodes = append(gs.Nodes, node)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("export nodes: %w", err)
	}
	if err := k.Clusters.Walk(ctx, nil, func(_ string, cluster types.Cluster) (bool, error) {
		gs.Clusters = append(gs.Clusters, cluster)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("export clusters: %w", err)
	}
	if err := k.Unbondings.Walk(ctx, nil, func(_ uint64, entry types.UnbondingEntry) (bool, error) {
		gs.Unbondings = append(gs.Unbondings, entry)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("export unbondings: %w", err)
	}
	if err := k.Revoked.Walk(ctx, nil, func(_ string, rev types.RevokedPubkey) (bool, error) {
		gs.RevokedPubkeys = append(gs.RevokedPubkeys, rev)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("export revoked pubkeys: %w", err)
	}
	if err := k.ServiceDays.Walk(ctx, nil, func(_ collections.Pair[string, uint64], day types.ServiceDay) (bool, error) {
		gs.ServiceDays = append(gs.ServiceDays, day)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("export service days: %w", err)
	}
	if err := k.Names.Walk(ctx, nil, func(_ string, claim types.NodeName) (bool, error) {
		gs.NodeNames = append(gs.NodeNames, claim)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("export node names: %w", err)
	}
	next, err := k.NextUnbonding.Peek(ctx)
	if err != nil {
		return nil, fmt.Errorf("export unbonding sequence: %w", err)
	}
	gs.NextUnbondingId = next
	if gs.Operators == nil {
		gs.Operators = []types.Operator{}
	}
	if gs.Nodes == nil {
		gs.Nodes = []types.Node{}
	}
	if gs.Clusters == nil {
		gs.Clusters = []types.Cluster{}
	}
	if gs.Unbondings == nil {
		gs.Unbondings = []types.UnbondingEntry{}
	}
	if gs.RevokedPubkeys == nil {
		gs.RevokedPubkeys = []types.RevokedPubkey{}
	}
	if gs.ServiceDays == nil {
		gs.ServiceDays = []types.ServiceDay{}
	}
	if gs.NodeNames == nil {
		gs.NodeNames = []types.NodeName{}
	}
	return gs, nil
}
