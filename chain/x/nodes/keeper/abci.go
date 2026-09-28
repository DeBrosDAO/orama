package keeper

import (
	"errors"
	"fmt"
	"sort"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// EndBlock pays matured unbonding entries and records one service day for
// operators who are active in this block. A calendar day is counted once,
// even if several blocks land in it; days with no block are not backfilled.
func (k Keeper) EndBlock(ctx sdk.Context) error {
	return k.transact(ctx, func(ctx sdk.Context) error {
		if err := k.completeUnbondings(ctx); err != nil {
			return err
		}
		return k.recordServiceDays(ctx)
	})
}

func (k Keeper) completeUnbondings(ctx sdk.Context) error {
	end := ctx.BlockTime().Unix()
	if end < 0 {
		return fmt.Errorf("block time is before the unix epoch")
	}
	// NewPrefixUntilPairRange(t) includes every pair whose time key is <= t:
	// the int64 prefix end is the next integer, and the iterator end is exclusive.
	var due []uint64
	err := k.UnbondingByTime.Walk(ctx, collections.NewPrefixUntilPairRange[int64, uint64](end), func(_ collections.Pair[int64, uint64], id uint64) (bool, error) {
		due = append(due, id)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("walk matured unbondings: %w", err)
	}
	for _, id := range due {
		entry, err := k.Unbondings.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("load matured unbonding %d: %w", id, err)
		}
		operator, err := sdk.AccAddressFromBech32(entry.Operator)
		if err != nil {
			return fmt.Errorf("unbonding %d operator: %w", id, err)
		}
		coins, err := norama(entry.Amount)
		if err != nil {
			return fmt.Errorf("unbonding %d: %w", id, err)
		}
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, operator, coins); err != nil {
			return fmt.Errorf("pay unbonding %d to %s: %w", id, entry.Operator, err)
		}
		if err := k.deleteUnbonding(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) recordServiceDays(ctx sdk.Context) error {
	unix := ctx.BlockTime().Unix()
	if unix < 0 {
		return fmt.Errorf("block time is before the unix epoch")
	}
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	day := uint64(unix / 86400)
	type qual struct {
		volume uint64
		relay  bool
	}
	found := make(map[string]*qual)
	err = k.Nodes.Walk(ctx, nil, func(_ string, node types.Node) (bool, error) {
		if node.Status != types.NodeStatusActive {
			return false, nil
		}
		storageMin, err := p.MinBondFor(types.RoleStorage)
		if err != nil {
			return true, err
		}
		relayMin, err := p.MinBondFor(types.RoleRelay)
		if err != nil {
			return true, err
		}
		storageOK := types.HasRole(node.Roles, types.RoleStorage) &&
			!bondOf(node, types.RoleStorage).LT(storageMin) &&
			node.DeclaredCapacityBytes >= p.MinServiceVolumeBytes
		relayOK := types.HasRole(node.Roles, types.RoleRelay) && !bondOf(node, types.RoleRelay).LT(relayMin)
		if !storageOK && !relayOK {
			return false, nil
		}
		q := found[node.Operator]
		if q == nil {
			q = &qual{}
			found[node.Operator] = q
		}
		if storageOK && node.DeclaredCapacityBytes > q.volume {
			q.volume = node.DeclaredCapacityBytes
		}
		if relayOK {
			q.relay = true
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("walk nodes for service days: %w", err)
	}
	operators := make([]string, 0, len(found))
	for operator := range found {
		operators = append(operators, operator)
	}
	sort.Strings(operators)
	for _, operator := range operators {
		q := found[operator]
		if q.volume < p.MinServiceVolumeBytes && !q.relay {
			continue
		}
		key := collections.Join(operator, day)
		existing, err := k.ServiceDays.Get(ctx, key)
		if err != nil {
			if !errors.Is(err, collections.ErrNotFound) {
				return fmt.Errorf("load service day %s/%d: %w", operator, day, err)
			}
			row := types.ServiceDay{Operator: operator, DayIndex: day, VolumeBytes: q.volume, Relay: q.relay}
			if err := k.ServiceDays.Set(ctx, key, row); err != nil {
				return fmt.Errorf("save service day %s/%d: %w", operator, day, err)
			}
			continue
		}
		changed := false
		if q.volume > existing.VolumeBytes {
			existing.VolumeBytes = q.volume
			changed = true
		}
		if q.relay && !existing.Relay {
			existing.Relay = true
			changed = true
		}
		if !changed {
			continue
		}
		if err := k.ServiceDays.Set(ctx, key, existing); err != nil {
			return fmt.Errorf("update service day %s/%d: %w", operator, day, err)
		}
	}
	return nil
}

// OperatorServiceDays returns how many distinct service days are recorded for
// an operator. A day is recorded only when the operator had an active STORAGE
// role at or above the volume floor, or an active RELAY role, in a block on
// that UTC day.
func (k Keeper) OperatorServiceDays(ctx sdk.Context, operator string) (uint64, error) {
	addr, err := types.CanonicalAddress(operator)
	if err != nil {
		return 0, err
	}
	var days uint64
	err = k.ServiceDays.Walk(ctx, collections.NewPrefixedPairRange[string, uint64](addr), func(collections.Pair[string, uint64], types.ServiceDay) (bool, error) {
		days++
		return false, nil
	})
	if err != nil {
		return 0, fmt.Errorf("count service days for %s: %w", addr, err)
	}
	return days, nil
}
