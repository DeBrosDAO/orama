package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// IsActive reports whether the node currently has at least one role bonded
// at its minimum and is not jailed, retired, or tombstoned.
func (k Keeper) IsActive(ctx sdk.Context, nodeID string) (bool, error) {
	node, err := k.GetNode(ctx, nodeID)
	if err != nil {
		return false, err
	}
	return node.Status == types.NodeStatusActive, nil
}

// IsRoleActive reports whether one role on the node is bonded at min_bond
// and the node itself is active.
func (k Keeper) IsRoleActive(ctx sdk.Context, nodeID string, role types.Role) (bool, error) {
	node, err := k.GetNode(ctx, nodeID)
	if err != nil {
		return false, err
	}
	if node.Status != types.NodeStatusActive || !types.HasRole(node.Roles, role) {
		return false, nil
	}
	p, err := k.params(ctx)
	if err != nil {
		return false, err
	}
	min, err := p.MinBondFor(role)
	if err != nil {
		return false, err
	}
	return !bondOf(node, role).LT(min), nil
}

// HotKey returns the node's hot key account. It does not require the node
// to be active; callers that need a live provider check IsActive as well.
func (k Keeper) HotKey(ctx sdk.Context, nodeID string) (string, error) {
	node, err := k.GetNode(ctx, nodeID)
	if err != nil {
		return "", err
	}
	return node.HotKey, nil
}

// Binding returns the current binding for a service on a node.
func (k Keeper) Binding(ctx sdk.Context, nodeID, service string) (types.Binding, error) {
	node, err := k.GetNode(ctx, nodeID)
	if err != nil {
		return types.Binding{}, err
	}
	for _, binding := range node.Bindings {
		if binding.Service == service {
			return binding, nil
		}
	}
	return types.Binding{}, fmt.Errorf("node %s has no binding for service %q: %w", nodeID, service, types.ErrNotFound)
}

// Jail marks a node jailed. A jailed node is not active and drops out of the
// free-capacity index. There is no admin message; later modules call this.
func (k Keeper) Jail(ctx sdk.Context, nodeID string) error {
	return k.transact(ctx, func(ctx sdk.Context) error {
		node, err := k.GetNode(ctx, nodeID)
		if err != nil {
			return err
		}
		if node.Status == types.NodeStatusRetired || node.Status == types.NodeStatusTombstoned {
			return fmt.Errorf("node %s is %s and cannot be jailed: %w", nodeID, node.Status, types.ErrNotActive)
		}
		if node.Status == types.NodeStatusJailed {
			return fmt.Errorf("node %s is already jailed: %w", nodeID, types.ErrNotActive)
		}
		node.Status = types.NodeStatusJailed
		if err := k.saveNode(ctx, node); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeJail,
			sdk.NewAttribute(types.AttributeNodeID, nodeID),
		))
		return nil
	})
}

// Unjail clears a jail. The node becomes active again only when a role is
// still bonded at its minimum. Retired and tombstoned nodes stay closed.
func (k Keeper) Unjail(ctx sdk.Context, nodeID string) error {
	return k.transact(ctx, func(ctx sdk.Context) error {
		node, err := k.GetNode(ctx, nodeID)
		if err != nil {
			return err
		}
		if node.Status != types.NodeStatusJailed {
			return fmt.Errorf("node %s is %s, not jailed", nodeID, node.Status)
		}
		p, err := k.params(ctx)
		if err != nil {
			return err
		}
		node.Status = types.NodeStatusRegistered
		if err := refreshStatus(&node, p); err != nil {
			return err
		}
		if err := k.saveNode(ctx, node); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeUnjail,
			sdk.NewAttribute(types.AttributeNodeID, nodeID),
		))
		return nil
	})
}

// Tombstone permanently closes a node and tombstones its bindings so those
// pubkeys cannot be registered again. Remaining bond is queued for unbonding.
func (k Keeper) Tombstone(ctx sdk.Context, nodeID string) error {
	return k.transact(ctx, func(ctx sdk.Context) error {
		node, err := k.GetNode(ctx, nodeID)
		if err != nil {
			return err
		}
		if err := k.retireNode(ctx, &node, types.NodeStatusTombstoned, types.RevocationTombstoned); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeTombstone,
			sdk.NewAttribute(types.AttributeNodeID, nodeID),
		))
		return nil
	})
}

// Slash burns fraction of a role's bond and of every unbonding entry for that
// role. fraction is in (0, 1]. When the node is not closed, declared capacity is
// clamped down to the new backing and reserved bytes are clamped to the clamped
// declaration: a slash is a penalty and never fails because the node is busy. The
// module that owns the reservation (x/storage) releases the replicas the smaller
// declaration cannot hold.
func (k Keeper) Slash(ctx sdk.Context, nodeID string, role types.Role, fraction math.LegacyDec) (math.Int, error) {
	var slashed math.Int
	err := k.transact(ctx, func(ctx sdk.Context) error {
		got, err := k.slash(ctx, nodeID, role, fraction)
		if err != nil {
			return err
		}
		slashed = got
		return nil
	})
	if err != nil {
		return math.Int{}, err
	}
	return slashed, nil
}

func (k Keeper) slash(ctx sdk.Context, nodeID string, role types.Role, fraction math.LegacyDec) (math.Int, error) {
	if !knownStoredRole(role) {
		return math.Int{}, fmt.Errorf("slash role %s is unknown", role)
	}
	node, err := k.GetNode(ctx, nodeID)
	if err != nil {
		return math.Int{}, err
	}
	if !types.HasRole(node.Roles, role) {
		return math.Int{}, fmt.Errorf("node %s does not have role %s: %w", nodeID, role, types.ErrNotActive)
	}
	p, err := k.params(ctx)
	if err != nil {
		return math.Int{}, err
	}
	bondCut, err := slashAmount(bondOf(node, role), fraction)
	if err != nil {
		return math.Int{}, err
	}
	entries, err := k.NodeUnbondings(ctx, nodeID)
	if err != nil {
		return math.Int{}, err
	}
	type change struct {
		entry  types.UnbondingEntry
		remove bool
	}
	var changes []change
	total := bondCut
	for _, entry := range entries {
		if entry.Role != role {
			continue
		}
		cut, err := slashAmount(entry.Amount, fraction)
		if err != nil {
			return math.Int{}, err
		}
		if cut.IsZero() {
			continue
		}
		total = total.Add(cut)
		entry.Amount = entry.Amount.Sub(cut)
		changes = append(changes, change{entry: entry, remove: entry.Amount.IsZero()})
	}
	if role == types.RoleStorage && node.Status != types.NodeStatusRetired && node.Status != types.NodeStatusTombstoned {
		nextBond := bondOf(node, role).Sub(bondCut)
		backed, err := types.BackedCapacity(nextBond, p)
		if err != nil {
			return math.Int{}, err
		}
		if node.DeclaredCapacityBytes > backed {
			node.DeclaredCapacityBytes = backed
		}
		if node.ReservedCapacityBytes > node.DeclaredCapacityBytes {
			node.ReservedCapacityBytes = node.DeclaredCapacityBytes
		}
	}
	if total.IsPositive() {
		coins, err := norama(total)
		if err != nil {
			return math.Int{}, err
		}
		if err := k.bankKeeper.BurnCoins(ctx, types.ModuleName, coins); err != nil {
			return math.Int{}, fmt.Errorf("burn slash of node %s: %w", nodeID, err)
		}
	}
	for _, change := range changes {
		if change.remove {
			if err := k.deleteUnbonding(ctx, change.entry); err != nil {
				return math.Int{}, err
			}
			continue
		}
		if err := k.Unbondings.Set(ctx, change.entry.Id, change.entry); err != nil {
			return math.Int{}, fmt.Errorf("update unbonding %d: %w", change.entry.Id, err)
		}
	}
	setBond(&node, role, bondOf(node, role).Sub(bondCut))
	if err := refreshStatus(&node, p); err != nil {
		return math.Int{}, err
	}
	if err := k.saveNode(ctx, node); err != nil {
		return math.Int{}, err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeSlash,
		sdk.NewAttribute(types.AttributeNodeID, nodeID),
		sdk.NewAttribute(types.AttributeRole, role.String()),
		sdk.NewAttribute(types.AttributeAmount, total.String()),
	))
	return total, nil
}

func knownStoredRole(role types.Role) bool {
	switch role {
	case types.RoleValidator, types.RoleStorage, types.RoleRelay, types.RoleExit, types.RoleDirauth, types.RoleArchiver:
		return true
	default:
		return false
	}
}

func slashAmount(amount math.Int, fraction math.LegacyDec) (math.Int, error) {
	if amount.IsNil() || amount.IsNegative() {
		return math.Int{}, fmt.Errorf("slash amount must be non-negative")
	}
	if fraction.IsNil() || !fraction.IsPositive() || fraction.GT(math.LegacyOneDec()) {
		return math.Int{}, fmt.Errorf("slash fraction must be in (0, 1], got %s", fraction)
	}
	if amount.IsZero() {
		return math.ZeroInt(), nil
	}
	cut := math.LegacyNewDecFromInt(amount).Mul(fraction).TruncateInt()
	if cut.IsNegative() || cut.GT(amount) {
		return math.Int{}, fmt.Errorf("slash cut %s is outside [0, %s]", cut, amount)
	}
	return cut, nil
}

// CreditRoleBond adds amount onto a role bond. The coins must already sit in
// the nodes module account (x/power's force-bond path sends them first). The
// call fails when the module balance does not cover the ledger plus amount.
func (k Keeper) CreditRoleBond(ctx sdk.Context, nodeID string, role types.Role, amount math.Int) error {
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := types.PositiveAmount(amount); err != nil {
			return fmt.Errorf("credit role bond: %w", err)
		}
		if !knownStoredRole(role) {
			return fmt.Errorf("credit role bond: unknown role %s", role)
		}
		node, err := k.GetNode(ctx, nodeID)
		if err != nil {
			return err
		}
		if err := closedNode(node); err != nil {
			return err
		}
		if !types.HasRole(node.Roles, role) {
			return fmt.Errorf("node %s does not have role %s", nodeID, role)
		}
		p, err := k.params(ctx)
		if err != nil {
			return err
		}
		bonds, unbonding, err := k.sumLedger(ctx)
		if err != nil {
			return err
		}
		need := bonds.Add(unbonding).Add(amount)
		have := k.moduleBalance(ctx)
		if have.LT(need) {
			return fmt.Errorf("module balance %s%s is short of bonded+unbonding %s%s plus credit %s%s",
				have, params.BaseDenom, bonds.Add(unbonding), params.BaseDenom, amount, params.BaseDenom)
		}
		setBond(&node, role, bondOf(node, role).Add(amount))
		if err := refreshStatus(&node, p); err != nil {
			return err
		}
		return k.saveNode(ctx, node)
	})
}
