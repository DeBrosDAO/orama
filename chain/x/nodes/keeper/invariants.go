package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// NodeInvariants is the result of CheckInvariants.
type NodeInvariants struct {
	BalanceMatches    bool
	ActiveRolesBonded bool
	CapacityBacked    bool
	Detail            string
}

// CheckInvariants checks the three x/nodes invariants from
// plans/open-network/track-c-chain.md C6:
//
//   - module balance == bonds + unbonding + name deposits
//   - active roles are bonded >= min_bond (and a registered node is not)
//   - declared capacity <= backed capacity, and the free-capacity index matches
func (k Keeper) CheckInvariants(ctx sdk.Context) (NodeInvariants, error) {
	p, err := k.params(ctx)
	if err != nil {
		return NodeInvariants{}, err
	}
	bonds, unbonding, err := k.sumLedger(ctx)
	if err != nil {
		return NodeInvariants{}, err
	}
	names, err := k.sumNameDeposits(ctx)
	if err != nil {
		return NodeInvariants{}, err
	}
	balance := k.moduleBalance(ctx)
	ledger := bonds.Add(unbonding).Add(names)
	balanceOK := balance.Equal(ledger)

	activeOK := true
	capacityOK := true
	var problems []string
	err = k.Nodes.Walk(ctx, nil, func(_ string, node types.Node) (bool, error) {
		meets, err := roleMeetsMin(node, p)
		if err != nil {
			return true, err
		}
		if node.Status == types.NodeStatusActive && !meets {
			activeOK = false
			problems = append(problems, fmt.Sprintf("node %s is active without a role at min_bond", node.NodeId))
		}
		if node.Status == types.NodeStatusRegistered && meets {
			activeOK = false
			problems = append(problems, fmt.Sprintf("node %s is registered with a role at min_bond", node.NodeId))
		}
		if !types.HasRole(node.Roles, types.RoleStorage) {
			if node.DeclaredCapacityBytes != 0 || node.ReservedCapacityBytes != 0 {
				capacityOK = false
				problems = append(problems, fmt.Sprintf("node %s has capacity without STORAGE", node.NodeId))
			}
		} else {
			backed, err := types.BackedCapacity(bondOf(node, types.RoleStorage), p)
			if err != nil {
				return true, err
			}
			if node.ReservedCapacityBytes > node.DeclaredCapacityBytes || node.DeclaredCapacityBytes > backed {
				capacityOK = false
				problems = append(problems, fmt.Sprintf(
					"node %s capacity declared=%d reserved=%d backed=%d",
					node.NodeId, node.DeclaredCapacityBytes, node.ReservedCapacityBytes, backed,
				))
			}
		}
		should := indexCapacity(&node)
		if should != node.CapacityIndexed {
			capacityOK = false
			problems = append(problems, fmt.Sprintf("node %s capacity index flag is %t, want %t", node.NodeId, node.CapacityIndexed, should))
		}
		if should {
			free := node.DeclaredCapacityBytes - node.ReservedCapacityBytes
			class := types.CapacityClass(free)
			if class != node.CapacityClass {
				capacityOK = false
				problems = append(problems, fmt.Sprintf("node %s capacity class %d, want %d", node.NodeId, node.CapacityClass, class))
			}
			got, err := k.FreeCapacity.Get(ctx, collections.Join3(class, node.Operator, node.NodeId))
			if err != nil || got != free {
				capacityOK = false
				problems = append(problems, fmt.Sprintf("node %s capacity index value %d, want %d (%v)", node.NodeId, got, free, err))
			}
		}
		return false, nil
	})
	if err != nil {
		return NodeInvariants{}, fmt.Errorf("walk nodes for invariants: %w", err)
	}

	detail := fmt.Sprintf(
		"balance match: %t (module=%s ledger=%s bonds=%s unbonding=%s name deposits=%s)\nactive roles bonded: %t\ncapacity backed: %t\n",
		balanceOK, balance, ledger, bonds, unbonding, names, activeOK, capacityOK,
	)
	for _, problem := range problems {
		detail += problem + "\n"
	}
	return NodeInvariants{
		BalanceMatches:    balanceOK,
		ActiveRolesBonded: activeOK,
		CapacityBacked:    capacityOK,
		Detail:            detail,
	}, nil
}

func (k Keeper) sumLedger(ctx sdk.Context) (math.Int, math.Int, error) {
	bonds := math.ZeroInt()
	err := k.Nodes.Walk(ctx, nil, func(_ string, node types.Node) (bool, error) {
		for _, bond := range node.Bonds {
			if bond.Amount.IsNil() || bond.Amount.IsNegative() {
				return true, fmt.Errorf("node %s bond for %s is invalid", node.NodeId, bond.Role)
			}
			bonds = bonds.Add(bond.Amount)
		}
		return false, nil
	})
	if err != nil {
		return math.Int{}, math.Int{}, fmt.Errorf("sum bonds: %w", err)
	}
	unbonding := math.ZeroInt()
	err = k.Unbondings.Walk(ctx, nil, func(_ uint64, entry types.UnbondingEntry) (bool, error) {
		if entry.Amount.IsNil() || !entry.Amount.IsPositive() {
			return true, fmt.Errorf("unbonding %d amount is invalid", entry.Id)
		}
		unbonding = unbonding.Add(entry.Amount)
		return false, nil
	})
	if err != nil {
		return math.Int{}, math.Int{}, fmt.Errorf("sum unbonding: %w", err)
	}
	return bonds, unbonding, nil
}

func roleMeetsMin(node types.Node, p types.Params) (bool, error) {
	for _, role := range node.Roles {
		min, err := p.MinBondFor(role)
		if err != nil {
			return false, err
		}
		if !bondOf(node, role).LT(min) {
			return true, nil
		}
	}
	return false, nil
}
