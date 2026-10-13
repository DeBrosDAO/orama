package types

import (
	"bytes"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
)

// MaxQueueRefusals is how many windows in a row a request's target may refuse before the queue
// gives up on it and refunds it. A payment that goes out, or a window that reaches the request
// and skips it without a refusal (capacity, per-address cap, a part below the minimum), resets
// the count.
const MaxQueueRefusals = 3

// MaxQueuedPerOwner is how many unshields one owner may have in the queue at once. A request over
// it is refused when it is made, so no address can fill the queue.
const MaxQueuedPerOwner = 10

// MaxFrontierLen bounds a stored frontier: position, leaf and one ommer per tree level.
const MaxFrontierLen = 8 + bundle.NodeLen + 32*bundle.NodeLen

// DefaultGenesisState is an empty pool and an empty tree with DefaultParams.
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Params:               DefaultParams(),
		NullifierAccumulator: make([]byte, bundle.NodeLen),
	}
}

func validAsset(asset []byte) error {
	if !bytes.Equal(asset, pool.NativeAsset[:]) {
		return fmt.Errorf("asset %x: only the native asset is active, multi-asset is off", asset)
	}
	return nil
}

// Validate checks genesis state that does not depend on the bank balance.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	if err := gs.validatePools(); err != nil {
		return err
	}
	if err := gs.validateQueue(); err != nil {
		return err
	}
	if err := gs.validateTree(); err != nil {
		return err
	}
	return gs.validateNullifiers()
}

type poolKey struct {
	vintage uint32
	asset   [bundle.NodeLen]byte
}

func (gs GenesisState) validatePools() error {
	pools := map[poolKey]bool{}
	for _, p := range gs.Pools {
		if err := validAsset(p.Asset); err != nil {
			return err
		}
		k := poolKey{vintage: p.Vintage}
		copy(k.asset[:], p.Asset)
		if p.Vintage == 0 || pools[k] {
			return fmt.Errorf("pool vintage %d is zero or duplicated", p.Vintage)
		}
		pools[k] = true
		if p.Balance.IsNil() || p.Balance.IsNegative() {
			return fmt.Errorf("pool %d balance must not be negative", p.Vintage)
		}
	}
	limiters := map[poolKey]bool{}
	for _, l := range gs.Limiters {
		k := poolKey{vintage: l.Vintage}
		copy(k.asset[:], l.Asset)
		if !pools[k] || limiters[k] {
			return fmt.Errorf("limiter for vintage %d has no pool or is duplicated", l.Vintage)
		}
		limiters[k] = true
		if l.Counted.IsNil() || l.Counted.IsNegative() {
			return fmt.Errorf("limiter %d counted must not be negative", l.Vintage)
		}
	}
	return nil
}

func (gs GenesisState) validateQueue() error {
	ids := map[uint64]bool{}
	owners := map[string]int{}
	for _, q := range gs.Queue {
		if ids[q.Id] || q.Id >= gs.NextQueueId {
			return fmt.Errorf("queued unshield %d is duplicated or not below next_queue_id", q.Id)
		}
		ids[q.Id] = true
		if q.Amount.IsNil() || !q.Amount.IsPositive() {
			return fmt.Errorf("queued unshield %d amount must be positive", q.Id)
		}
		owner, err := sdk.AccAddressFromBech32(q.Owner)
		if err != nil {
			return fmt.Errorf("queued unshield %d owner: %w", q.Id, err)
		}
		if q.Owner != owner.String() {
			return fmt.Errorf("queued unshield %d owner %s is not in canonical form %s", q.Id, q.Owner, owner)
		}
		if owners[owner.String()]++; owners[owner.String()] > MaxQueuedPerOwner {
			return fmt.Errorf("%w: %s has more than %d queued unshields", ErrQueueOwnerFull, q.Owner, MaxQueuedPerOwner)
		}
		// A row reaches MaxQueueRefusals and stays only when its refund itself was refused; it is
		// retried next window, so an export may hold it. Above that no path writes.
		if q.Refusals > MaxQueueRefusals {
			return fmt.Errorf("queued unshield %d has been refused %d times; at %d it is refunded and leaves the queue", q.Id, q.Refusals, MaxQueueRefusals)
		}
		if q.Target != UnshieldTargetBond && q.Target != UnshieldTargetNodeBond {
			return fmt.Errorf("queued unshield %d has target %s; only bond targets queue", q.Id, q.Target)
		}
		if err := validAsset(q.Asset); err != nil {
			return err
		}
	}
	return nil
}

func (gs GenesisState) validateTree() error {
	empty := len(gs.Frontier) == 0
	if empty != (gs.TreeSize == 0) || empty != (len(gs.CurrentRoot) == 0) {
		return fmt.Errorf("frontier, tree_size and current_root must all be empty or all be set")
	}
	if len(gs.Frontier) > MaxFrontierLen {
		return fmt.Errorf("frontier is %d bytes, the limit is %d", len(gs.Frontier), MaxFrontierLen)
	}
	if !empty && len(gs.CurrentRoot) != bundle.NodeLen {
		return fmt.Errorf("current_root must be %d bytes", bundle.NodeLen)
	}
	roots := map[string]bool{}
	for _, a := range gs.Anchors {
		if len(a.Root) != bundle.NodeLen || a.Height < 0 || roots[string(a.Root)] {
			return fmt.Errorf("anchor %x is malformed or duplicated", a.Root)
		}
		roots[string(a.Root)] = true
	}
	return gs.validateSubtreeRoots()
}

// validateSubtreeRoots checks that the tree holds a root for each filled subtree, in order from
// index 0 with no gap, and for no other.
func (gs GenesisState) validateSubtreeRoots() error {
	if want := gs.TreeSize / SubtreeLeaves; uint64(len(gs.SubtreeRoots)) != want {
		return fmt.Errorf("tree_size %d fills %d subtrees but %d subtree roots are listed", gs.TreeSize, want, len(gs.SubtreeRoots))
	}
	for i, r := range gs.SubtreeRoots {
		if r.Index != uint64(i) || len(r.Root) != bundle.NodeLen || r.Height < 0 {
			return fmt.Errorf("subtree root %d (index %d) is malformed or out of order", i, r.Index)
		}
	}
	return nil
}

func (gs GenesisState) validateNullifiers() error {
	if len(gs.NullifierAccumulator) != bundle.NodeLen {
		return fmt.Errorf("nullifier_accumulator must be %d bytes", bundle.NodeLen)
	}
	if gs.NullifierCount != uint64(len(gs.Nullifiers)) {
		return fmt.Errorf("nullifier_count is %d but %d nullifiers are listed", gs.NullifierCount, len(gs.Nullifiers))
	}
	var acc [bundle.NodeLen]byte
	seen := make(map[[bundle.NodeLen]byte]bool, len(gs.Nullifiers))
	for _, raw := range gs.Nullifiers {
		var nf [bundle.NodeLen]byte
		if len(raw) != bundle.NodeLen {
			return fmt.Errorf("nullifier %x is not %d bytes", raw, bundle.NodeLen)
		}
		copy(nf[:], raw)
		if seen[nf] {
			return fmt.Errorf("nullifier %x is listed twice", nf)
		}
		seen[nf] = true
		acc = nullifier.Fold(acc, nf)
	}
	if !bytes.Equal(acc[:], gs.NullifierAccumulator) {
		return fmt.Errorf("nullifier_accumulator does not match the listed nullifiers")
	}
	return nil
}
