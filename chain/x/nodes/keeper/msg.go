package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// RegisterOperator records the signer as an operator. Operators are the only
// accounts that can register nodes or optional cluster rows.
func (k Keeper) RegisterOperator(ctx sdk.Context, msg *types.MsgRegisterOperator) error {
	if msg == nil {
		return fmt.Errorf("nil MsgRegisterOperator")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		addr, err := types.CanonicalAddress(msg.Operator)
		if err != nil {
			return err
		}
		has, err := k.Operators.Has(ctx, addr)
		if err != nil {
			return fmt.Errorf("load operator %s: %w", addr, err)
		}
		if has {
			return fmt.Errorf("operator %s: %w", addr, types.ErrExists)
		}
		if owner, err := k.HotKeys.Get(ctx, addr); err == nil {
			return fmt.Errorf("account %s is the hot key of node %s and cannot be an operator: %w", addr, owner, types.ErrHotKey)
		} else if !errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("load hot key %s: %w", addr, err)
		}
		op := types.Operator{
			Address:            addr,
			RegisteredAtHeight: ctx.BlockHeight(),
			RegisteredAtUnix:   ctx.BlockTime().Unix(),
		}
		if err := k.Operators.Set(ctx, addr, op); err != nil {
			return fmt.Errorf("save operator %s: %w", addr, err)
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeRegisterOperator,
			sdk.NewAttribute(types.AttributeOperator, addr),
		))
		return nil
	})
}

// RegisterNode creates a global node. Every binding signature is checked
// against this chain id, and each service pubkey must be new.
func (k Keeper) RegisterNode(ctx sdk.Context, msg *types.MsgRegisterNode) error {
	if msg == nil {
		return fmt.Errorf("nil MsgRegisterNode")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		p, err := k.params(ctx)
		if err != nil {
			return err
		}
		operator, err := types.CanonicalAddress(msg.Operator)
		if err != nil {
			return err
		}
		if err := k.requireOperator(ctx, operator); err != nil {
			return err
		}
		has, err := k.Nodes.Has(ctx, msg.NodeId)
		if err != nil {
			return fmt.Errorf("load node %s: %w", msg.NodeId, err)
		}
		if has {
			return fmt.Errorf("node %s: %w", msg.NodeId, types.ErrExists)
		}
		hot, err := types.CanonicalAddress(msg.HotKey)
		if err != nil {
			return fmt.Errorf("hot key: %w", err)
		}
		if err := k.assertHotKeyAvailable(ctx, hot, operator, msg.NodeId); err != nil {
			return err
		}
		if err := types.ValidateEndpoints(msg.Endpoints, 0, p.MaxEndpoints); err != nil {
			return err
		}
		if err := k.assertIPsAvailable(ctx, types.LiteralIPs(msg.Endpoints), msg.NodeId); err != nil {
			return err
		}
		if err := k.verifyBindings(ctx, operator, msg.Bindings, p); err != nil {
			return err
		}
		if err := types.CheckHotKeyBinding(hot, msg.Bindings); err != nil {
			return err
		}
		if err := types.CheckConsensusBinding(msg.Bindings); err != nil {
			return err
		}
		for _, binding := range msg.Bindings {
			if err := k.assertPubkeyAvailable(ctx, binding.Pubkey, ""); err != nil {
				return err
			}
		}
		node := types.Node{
			NodeId:             msg.NodeId,
			Operator:           operator,
			Roles:              append([]types.Role(nil), msg.Roles...),
			HotKey:             hot,
			Bindings:           cloneBindings(msg.Bindings),
			Endpoints:          cloneStrings(msg.Endpoints),
			RegionHint:         msg.RegionHint,
			Asn:                msg.Asn,
			Status:             types.NodeStatusRegistered,
			RegisteredAtHeight: ctx.BlockHeight(),
			IdentitySinceUnix:  ctx.BlockTime().Unix(),
		}
		owner, err := sdk.AccAddressFromBech32(operator)
		if err != nil {
			return fmt.Errorf("operator %s: %w", operator, err)
		}
		want, err := nodeChargeBytes(node)
		if err != nil {
			return err
		}
		got, parts, err := k.chargeGrowth(ctx, owner, func(part uint32) string {
			return nodeDepositID(node.NodeId, part)
		}, 0, 0, want)
		if err != nil {
			return err
		}
		node.DepositBytes = got
		node.DepositParts = parts
		for _, binding := range node.Bindings {
			if err := k.LivePubkeys.Set(ctx, pubkeyKey(binding.Pubkey), node.NodeId); err != nil {
				return fmt.Errorf("index pubkey for node %s: %w", node.NodeId, err)
			}
		}
		if err := k.indexHotKey(ctx, "", node.HotKey, node.NodeId); err != nil {
			return err
		}
		if err := k.indexIPs(ctx, nil, types.LiteralIPs(node.Endpoints), node.NodeId); err != nil {
			return err
		}
		if err := k.saveNode(ctx, node); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeRegisterNode,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeNodeID, node.NodeId),
		))
		return nil
	})
}

// UpdateNode rotates the hot key and bindings. The signer must be the
// node's operator. Replaced service pubkeys are retired and cannot be reused.
func (k Keeper) UpdateNode(ctx sdk.Context, msg *types.MsgUpdateNode) error {
	if msg == nil {
		return fmt.Errorf("nil MsgUpdateNode")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		if msg.HotKey == "" && len(msg.Bindings) == 0 && !msg.SetEndpoints && !msg.SetRegionHint && !msg.SetAsn {
			return fmt.Errorf("node %s update changes nothing", msg.NodeId)
		}
		p, err := k.params(ctx)
		if err != nil {
			return err
		}
		node, operator, err := k.requireNodeOwner(ctx, msg.NodeId, msg.Operator)
		if err != nil {
			return err
		}
		if err := closedNode(node); err != nil {
			return err
		}
		before := node
		if err := k.applyNodeUpdate(ctx, &node, operator, msg, p); err != nil {
			return err
		}
		if err := types.CheckHotKeyBinding(node.HotKey, node.Bindings); err != nil {
			return err
		}
		if err := types.CheckConsensusBinding(node.Bindings); err != nil {
			return err
		}
		if err := k.reindexNode(ctx, before, node); err != nil {
			return err
		}
		if err := k.chargeNode(ctx, &node); err != nil {
			return err
		}
		if err := k.saveNode(ctx, node); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeUpdateNode,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeNodeID, node.NodeId),
		))
		return nil
	})
}

// applyNodeUpdate applies msg's changes to node in memory. It checks what needs state (hot key
// availability, endpoint address ownership, binding signatures) but writes nothing. It restarts the
// network identity lock when the ASN or the derived /16 changes.
func (k Keeper) applyNodeUpdate(ctx sdk.Context, node *types.Node, operator string, msg *types.MsgUpdateNode, p types.Params) error {
	before := *node
	if msg.HotKey != "" {
		hot, err := types.CanonicalAddress(msg.HotKey)
		if err != nil {
			return fmt.Errorf("hot key: %w", err)
		}
		if err := k.assertHotKeyAvailable(ctx, hot, operator, node.NodeId); err != nil {
			return err
		}
		node.HotKey = hot
	}
	if len(msg.Bindings) > 0 {
		if err := k.replaceBindings(ctx, node, operator, msg.Bindings, p); err != nil {
			return err
		}
	}
	if msg.SetEndpoints {
		if err := types.ValidateEndpoints(msg.Endpoints, 0, p.MaxEndpoints); err != nil {
			return err
		}
		if err := k.assertIPsAvailable(ctx, types.LiteralIPs(msg.Endpoints), node.NodeId); err != nil {
			return err
		}
		node.Endpoints = cloneStrings(msg.Endpoints)
	}
	if msg.SetRegionHint {
		node.RegionHint = msg.RegionHint
	}
	if msg.SetAsn {
		node.Asn = msg.Asn
	}
	if node.Asn != before.Asn || types.NetworkOf(node.Endpoints) != types.NetworkOf(before.Endpoints) {
		node.IdentitySinceUnix = ctx.BlockTime().Unix()
	}
	return nil
}

// reindexNode brings the hot key and endpoint address indexes in line with an updated node.
func (k Keeper) reindexNode(ctx sdk.Context, before types.Node, after types.Node) error {
	if err := k.indexHotKey(ctx, before.HotKey, after.HotKey, after.NodeId); err != nil {
		return err
	}
	if err := k.indexIPs(ctx, before.Endpoints, types.LiteralIPs(after.Endpoints), after.NodeId); err != nil {
		return err
	}
	return nil
}

func (k Keeper) replaceBindings(ctx sdk.Context, node *types.Node, operator string, bindings []types.Binding, p types.Params) error {
	if err := k.verifyBindings(ctx, operator, bindings, p); err != nil {
		return err
	}
	for _, binding := range bindings {
		if err := k.assertPubkeyAvailable(ctx, binding.Pubkey, node.NodeId); err != nil {
			return err
		}
	}
	next := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		next[pubkeyKey(binding.Pubkey)] = struct{}{}
	}
	prev := make(map[string]struct{}, len(node.Bindings))
	for _, binding := range node.Bindings {
		key := pubkeyKey(binding.Pubkey)
		prev[key] = struct{}{}
		if _, keep := next[key]; keep {
			continue
		}
		if err := k.revokeBinding(ctx, binding, node.NodeId, types.RevocationRetired); err != nil {
			return err
		}
	}
	cloned := cloneBindings(bindings)
	for _, binding := range cloned {
		key := pubkeyKey(binding.Pubkey)
		if _, already := prev[key]; already {
			continue
		}
		if err := k.LivePubkeys.Set(ctx, key, node.NodeId); err != nil {
			return fmt.Errorf("index pubkey for node %s: %w", node.NodeId, err)
		}
	}
	node.Bindings = cloned
	return nil
}

// RetireNode stops a node, retires its bindings, and queues whatever bond
// remains. Reserved storage must already be released.
func (k Keeper) RetireNode(ctx sdk.Context, msg *types.MsgRetireNode) error {
	if msg == nil {
		return fmt.Errorf("nil MsgRetireNode")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		node, operator, err := k.requireNodeOwner(ctx, msg.NodeId, msg.Operator)
		if err != nil {
			return err
		}
		if err := k.retireNode(ctx, &node, types.NodeStatusRetired, types.RevocationRetired); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeRetireNode,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeNodeID, node.NodeId),
		))
		return nil
	})
}

func (k Keeper) retireNode(ctx sdk.Context, node *types.Node, status types.NodeStatus, reason types.Revocation) error {
	if node.Status == types.NodeStatusRetired || node.Status == types.NodeStatusTombstoned {
		return fmt.Errorf("node %s is %s", node.NodeId, node.Status)
	}
	if node.ReservedCapacityBytes != 0 {
		return fmt.Errorf("node %s still has %d reserved bytes: %w", node.NodeId, node.ReservedCapacityBytes, types.ErrReserved)
	}
	for _, binding := range node.Bindings {
		if err := k.revokeBinding(ctx, binding, node.NodeId, reason); err != nil {
			return err
		}
	}
	if err := k.releaseIdentity(ctx, *node); err != nil {
		return err
	}
	if _, err := k.releaseNodeName(ctx, node.NodeId, retireReason(status)); err != nil {
		return err
	}
	node.Bindings = nil
	for _, role := range node.Roles {
		amount := bondOf(*node, role)
		if !amount.IsPositive() {
			continue
		}
		if err := k.enqueueUnbonding(ctx, *node, role, amount); err != nil {
			return err
		}
		setBond(node, role, math.ZeroInt())
	}
	node.DeclaredCapacityBytes = 0
	node.Status = status
	if err := k.releaseParts(ctx, func(part uint32) string {
		return nodeDepositID(node.NodeId, part)
	}, node.DepositParts, node.DepositBytes); err != nil {
		return err
	}
	node.DepositBytes = 0
	node.DepositParts = 0
	return k.saveNode(ctx, *node)
}

// BondNode escrows norama from the operator into the node's role bond.
func (k Keeper) BondNode(ctx sdk.Context, msg *types.MsgBondNode) error {
	if msg == nil {
		return fmt.Errorf("nil MsgBondNode")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		p, err := k.params(ctx)
		if err != nil {
			return err
		}
		node, operator, err := k.requireNodeOwner(ctx, msg.NodeId, msg.Operator)
		if err != nil {
			return err
		}
		if err := closedNode(node); err != nil {
			return err
		}
		if !types.HasRole(node.Roles, msg.Role) {
			return fmt.Errorf("node %s does not have role %s", node.NodeId, msg.Role)
		}
		owner, err := sdk.AccAddressFromBech32(operator)
		if err != nil {
			return fmt.Errorf("operator %s: %w", operator, err)
		}
		coins, err := norama(msg.Amount)
		if err != nil {
			return err
		}
		// Funding the bond from the operator's own earnings happens here, inside the message's
		// own branch, and only after every check above passed: BaseApp discards this branch when
		// the message fails, so a rejected bond can never leave earnings turned into a bank balance.
		if err := k.earningsKeeper.FundSpendFromEarnings(ctx, owner, params.BaseDenom, msg.Amount); err != nil {
			return fmt.Errorf("bond node %s role %s: %w", node.NodeId, msg.Role, err)
		}
		if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, owner, types.ModuleName, coins); err != nil {
			return fmt.Errorf("bond node %s role %s: %w", node.NodeId, msg.Role, err)
		}
		setBond(&node, msg.Role, bondOf(node, msg.Role).Add(msg.Amount))
		if err := refreshStatus(&node, p); err != nil {
			return err
		}
		if err := k.saveNode(ctx, node); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeBondNode,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeNodeID, node.NodeId),
			sdk.NewAttribute(types.AttributeRole, msg.Role.String()),
			sdk.NewAttribute(types.AttributeAmount, msg.Amount.String()),
		))
		return nil
	})
}

// UnbondNode moves norama from a role bond into the unbonding queue. It
// refuses a reduction that would leave declared capacity above the remaining
// STORAGE bond.
func (k Keeper) UnbondNode(ctx sdk.Context, msg *types.MsgUnbondNode) error {
	if msg == nil {
		return fmt.Errorf("nil MsgUnbondNode")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		p, err := k.params(ctx)
		if err != nil {
			return err
		}
		node, operator, err := k.requireNodeOwner(ctx, msg.NodeId, msg.Operator)
		if err != nil {
			return err
		}
		if err := closedNode(node); err != nil {
			return err
		}
		if !types.HasRole(node.Roles, msg.Role) {
			return fmt.Errorf("node %s does not have role %s", node.NodeId, msg.Role)
		}
		have := bondOf(node, msg.Role)
		if msg.Amount.GT(have) {
			return fmt.Errorf("unbond %s exceeds %s bond %s on node %s", msg.Amount, msg.Role, have, node.NodeId)
		}
		next := have.Sub(msg.Amount)
		if msg.Role == types.RoleStorage {
			backed, err := types.BackedCapacity(next, p)
			if err != nil {
				return err
			}
			if node.DeclaredCapacityBytes > backed {
				return fmt.Errorf("unbond would leave declared capacity %d above backed %d: %w", node.DeclaredCapacityBytes, backed, types.ErrCapacity)
			}
		}
		setBond(&node, msg.Role, next)
		if err := k.enqueueUnbonding(ctx, node, msg.Role, msg.Amount); err != nil {
			return err
		}
		if err := refreshStatus(&node, p); err != nil {
			return err
		}
		if err := k.saveNode(ctx, node); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeUnbondNode,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeNodeID, node.NodeId),
			sdk.NewAttribute(types.AttributeRole, msg.Role.String()),
			sdk.NewAttribute(types.AttributeAmount, msg.Amount.String()),
		))
		return nil
	})
}

// DeclareCapacity sets a STORAGE node's declared bytes, capped by
// bond / bond_per_gib, or by the probation cap when the STORAGE bond is zero.
func (k Keeper) DeclareCapacity(ctx sdk.Context, msg *types.MsgDeclareCapacity) error {
	if msg == nil {
		return fmt.Errorf("nil MsgDeclareCapacity")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		p, err := k.params(ctx)
		if err != nil {
			return err
		}
		node, operator, err := k.requireNodeOwner(ctx, msg.NodeId, msg.Operator)
		if err != nil {
			return err
		}
		if err := closedNode(node); err != nil {
			return err
		}
		if !types.HasRole(node.Roles, types.RoleStorage) {
			return fmt.Errorf("node %s does not have the STORAGE role", node.NodeId)
		}
		if msg.CapacityBytes < node.ReservedCapacityBytes {
			return fmt.Errorf("declared %d is below reserved %d: %w", msg.CapacityBytes, node.ReservedCapacityBytes, types.ErrReserved)
		}
		backed, err := types.BackedCapacity(bondOf(node, types.RoleStorage), p)
		if err != nil {
			return err
		}
		if msg.CapacityBytes > backed {
			return fmt.Errorf("declared capacity %d exceeds backed %d: %w", msg.CapacityBytes, backed, types.ErrCapacity)
		}
		node.DeclaredCapacityBytes = msg.CapacityBytes
		if err := k.chargeNode(ctx, &node); err != nil {
			return err
		}
		if err := k.saveNode(ctx, node); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeDeclareCapacity,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeNodeID, node.NodeId),
			sdk.NewAttribute(types.AttributeAmount, fmt.Sprintf("%d", msg.CapacityBytes)),
		))
		return nil
	})
}

func (k Keeper) chargeNode(ctx sdk.Context, node *types.Node) error {
	owner, err := sdk.AccAddressFromBech32(node.Operator)
	if err != nil {
		return fmt.Errorf("operator %s: %w", node.Operator, err)
	}
	want, err := nodeChargeBytes(*node)
	if err != nil {
		return err
	}
	got, parts, err := k.chargeGrowth(ctx, owner, func(part uint32) string {
		return nodeDepositID(node.NodeId, part)
	}, node.DepositBytes, node.DepositParts, want)
	if err != nil {
		return err
	}
	node.DepositBytes = got
	node.DepositParts = parts
	return nil
}

// RegisterCluster adds an optional public registry row. It does not attach
// any node to the cluster and stores no private address or secret (D1).
func (k Keeper) RegisterCluster(ctx sdk.Context, msg *types.MsgRegisterCluster) error {
	if msg == nil {
		return fmt.Errorf("nil MsgRegisterCluster")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		p, err := k.params(ctx)
		if err != nil {
			return err
		}
		operator, err := types.CanonicalAddress(msg.Operator)
		if err != nil {
			return err
		}
		if err := k.requireOperator(ctx, operator); err != nil {
			return err
		}
		if err := types.ValidateEndpoints(msg.PublicEndpoints, 1, p.MaxEndpoints); err != nil {
			return err
		}
		has, err := k.Clusters.Has(ctx, msg.ClusterId)
		if err != nil {
			return fmt.Errorf("load cluster %s: %w", msg.ClusterId, err)
		}
		if has {
			return fmt.Errorf("cluster %s: %w", msg.ClusterId, types.ErrExists)
		}
		cluster := types.Cluster{
			ClusterId:       msg.ClusterId,
			Operator:        operator,
			BaseDomain:      msg.BaseDomain,
			PublicEndpoints: cloneStrings(msg.PublicEndpoints),
			MetadataUri:     msg.MetadataUri,
			Status:          types.ClusterStatusActive,
		}
		if err := k.chargeCluster(ctx, &cluster); err != nil {
			return err
		}
		if err := k.Clusters.Set(ctx, cluster.ClusterId, cluster); err != nil {
			return fmt.Errorf("save cluster %s: %w", cluster.ClusterId, err)
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeRegisterCluster,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeCluster, cluster.ClusterId),
		))
		return nil
	})
}

// UpdateCluster replaces a cluster row's public fields. The signer must own it.
func (k Keeper) UpdateCluster(ctx sdk.Context, msg *types.MsgUpdateCluster) error {
	if msg == nil {
		return fmt.Errorf("nil MsgUpdateCluster")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		p, err := k.params(ctx)
		if err != nil {
			return err
		}
		operator, err := types.CanonicalAddress(msg.Operator)
		if err != nil {
			return err
		}
		cluster, err := k.GetCluster(ctx, msg.ClusterId)
		if err != nil {
			return err
		}
		if cluster.Operator != operator {
			return fmt.Errorf("cluster %s: %w", cluster.ClusterId, types.ErrUnauthorized)
		}
		if cluster.Status != types.ClusterStatusActive {
			return fmt.Errorf("cluster %s is %s", cluster.ClusterId, cluster.Status)
		}
		if err := types.ValidateEndpoints(msg.PublicEndpoints, 1, p.MaxEndpoints); err != nil {
			return err
		}
		cluster.BaseDomain = msg.BaseDomain
		cluster.PublicEndpoints = cloneStrings(msg.PublicEndpoints)
		cluster.MetadataUri = msg.MetadataUri
		if err := k.chargeCluster(ctx, &cluster); err != nil {
			return err
		}
		if err := k.Clusters.Set(ctx, cluster.ClusterId, cluster); err != nil {
			return fmt.Errorf("save cluster %s: %w", cluster.ClusterId, err)
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeUpdateCluster,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeCluster, cluster.ClusterId),
		))
		return nil
	})
}

// RetireCluster marks a registry row retired and releases its deposit.
// The id cannot be registered again.
func (k Keeper) RetireCluster(ctx sdk.Context, msg *types.MsgRetireCluster) error {
	if msg == nil {
		return fmt.Errorf("nil MsgRetireCluster")
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := msg.ValidateBasic(); err != nil {
			return err
		}
		operator, err := types.CanonicalAddress(msg.Operator)
		if err != nil {
			return err
		}
		cluster, err := k.GetCluster(ctx, msg.ClusterId)
		if err != nil {
			return err
		}
		if cluster.Operator != operator {
			return fmt.Errorf("cluster %s: %w", cluster.ClusterId, types.ErrUnauthorized)
		}
		if cluster.Status != types.ClusterStatusActive {
			return fmt.Errorf("cluster %s is %s", cluster.ClusterId, cluster.Status)
		}
		if err := k.releaseParts(ctx, func(part uint32) string {
			return clusterDepositID(cluster.ClusterId, part)
		}, cluster.DepositParts, cluster.DepositBytes); err != nil {
			return err
		}
		cluster.DepositBytes = 0
		cluster.DepositParts = 0
		cluster.Status = types.ClusterStatusRetired
		if err := k.Clusters.Set(ctx, cluster.ClusterId, cluster); err != nil {
			return fmt.Errorf("save cluster %s: %w", cluster.ClusterId, err)
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeRetireCluster,
			sdk.NewAttribute(types.AttributeOperator, operator),
			sdk.NewAttribute(types.AttributeCluster, cluster.ClusterId),
		))
		return nil
	})
}

func (k Keeper) chargeCluster(ctx sdk.Context, cluster *types.Cluster) error {
	owner, err := sdk.AccAddressFromBech32(cluster.Operator)
	if err != nil {
		return fmt.Errorf("operator %s: %w", cluster.Operator, err)
	}
	want, err := clusterChargeBytes(*cluster)
	if err != nil {
		return err
	}
	got, parts, err := k.chargeGrowth(ctx, owner, func(part uint32) string {
		return clusterDepositID(cluster.ClusterId, part)
	}, cluster.DepositBytes, cluster.DepositParts, want)
	if err != nil {
		return err
	}
	cluster.DepositBytes = got
	cluster.DepositParts = parts
	return nil
}

// retireReason names why a closing node releases its name.
func retireReason(status types.NodeStatus) string {
	if status == types.NodeStatusTombstoned {
		return releaseReasonTombstone
	}
	return releaseReasonRetire
}
