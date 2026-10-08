// Package repair is the repair delegate: a service a deal names so a slot
// that loses its provider gets its replica back while the client is away.
// It holds only the repair seed of each deal it serves. It fetches a
// surviving replica, strips that slot's outer layer, applies the new slot's
// layer, checks the result against the new slot's piece root, and uploads it
// to the new provider. It never sees the plaintext, and x/storage never
// assigns a slot of such a deal to the delegate's operator.
package repair

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/storagekey"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

var (
	// ErrNotDelegate is a deal that names another repair delegate.
	ErrNotDelegate = errors.New("deal does not name this repair delegate")
	// ErrOwnSlot is a slot assigned to the delegate's own operator. x/storage
	// must never do that; the delegate refuses rather than repair it.
	ErrOwnSlot = errors.New("slot is assigned to the repair delegate's operator")
	// ErrRootMismatch is a rewrapped replica whose root is not the slot's
	// root: the repair seed is not this deal's.
	ErrRootMismatch = errors.New("rewrapped replica does not match the slot root")
	// ErrNoSource is a slot to repair with no surviving replica to copy.
	ErrNoSource = errors.New("no surviving replica could be fetched")
)

// Chain is the state the delegate reads.
type Chain interface {
	Deal(ctx context.Context, id uint64) (types.Deal, error)
	Slot(ctx context.Context, dealID uint64, slot uint32) (types.Slot, error)
	ProviderURL(ctx context.Context, nodeID string) (string, error)
	// Height is the chain's latest block height.
	Height(ctx context.Context) (int64, error)
}

// Transport moves pieces to and from providers.
type Transport interface {
	Fetch(ctx context.Context, base string, root []byte) ([]byte, error)
	Upload(ctx context.Context, base string, root, body []byte) error
}

// Delegate repairs the deals whose repair seeds it holds.
type Delegate struct {
	chain    Chain
	net      Transport
	operator string
}

// New returns a delegate acting for operator, the address deals name as
// repair_delegate.
func New(chain Chain, net Transport, operator string) (*Delegate, error) {
	if chain == nil || net == nil || operator == "" {
		return nil, errors.New("repair delegate needs a chain, a transport, and its operator")
	}
	return &Delegate{chain: chain, net: net, operator: operator}, nil
}

// Repaired is one slot the delegate uploaded.
type Repaired struct {
	DealID   uint64
	Slot     uint32
	From     uint32
	Provider string
	// BlocksSinceAssigned is how many blocks passed between the chain
	// assigning the replacement slot (the eviction) and the upload that
	// restores the replica. It is the delegate's share of the time to restore
	// the full replica count; the new provider's acceptance follows.
	BlocksSinceAssigned int64
}

// RepairDeal uploads every assigned-but-unaccepted slot of dealID that it
// can rebuild from an accepted replica. A new deal whose client has not
// uploaded yet has no accepted replica and is left alone.
func (d *Delegate) RepairDeal(ctx context.Context, dealID uint64, repairSeed []byte) ([]Repaired, error) {
	deal, err := d.chain.Deal(ctx, dealID)
	if err != nil {
		return nil, err
	}
	if deal.RepairDelegate != d.operator {
		return nil, fmt.Errorf("deal %d: %w", dealID, ErrNotDelegate)
	}
	var sources, targets []types.Slot
	for i := uint32(0); i < deal.Replicas; i++ {
		slot, err := d.chain.Slot(ctx, dealID, i)
		if err != nil {
			return nil, err
		}
		if slot.Operator == d.operator && slot.NodeId != "" {
			return nil, fmt.Errorf("deal %d slot %d: %w", dealID, i, ErrOwnSlot)
		}
		switch {
		case slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE && slot.Accepted:
			sources = append(sources, slot)
		case slot.Status == types.SlotStatus_SLOT_STATUS_ASSIGNED && !slot.Accepted:
			targets = append(targets, slot)
		}
	}
	if len(sources) == 0 {
		return nil, nil
	}
	var done []Repaired
	var errs []error
	for _, target := range targets {
		r, err := d.repairSlot(ctx, deal, repairSeed, sources, target)
		if err != nil {
			errs = append(errs, fmt.Errorf("deal %d slot %d: %w", dealID, target.Index, err))
			continue
		}
		done = append(done, r)
	}
	return done, errors.Join(errs...)
}

func (d *Delegate) repairSlot(ctx context.Context, deal types.Deal, seed []byte, sources []types.Slot, target types.Slot) (Repaired, error) {
	dest, err := d.chain.ProviderURL(ctx, target.NodeId)
	if err != nil {
		return Repaired{}, err
	}
	var errs []error
	for _, src := range sources {
		body, err := d.rebuild(ctx, deal, seed, src, target)
		if errors.Is(err, ErrRootMismatch) || errors.Is(err, storagekey.ErrSeed) {
			return Repaired{}, err
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("from slot %d: %w", src.Index, err))
			continue
		}
		height, err := d.chain.Height(ctx)
		if err != nil {
			return Repaired{}, fmt.Errorf("read chain height: %w", err)
		}
		if err := d.net.Upload(ctx, dest, target.PieceRoot, body); err != nil {
			return Repaired{}, fmt.Errorf("upload to %s: %w", target.NodeId, err)
		}
		return Repaired{DealID: deal.Id, Slot: target.Index, From: src.Index, Provider: target.NodeId,
			BlocksSinceAssigned: height - target.AssignHeight}, nil
	}
	return Repaired{}, fmt.Errorf("%w: %w", ErrNoSource, errors.Join(errs...))
}

func (d *Delegate) rebuild(ctx context.Context, deal types.Deal, seed []byte, src, target types.Slot) ([]byte, error) {
	base, err := d.chain.ProviderURL(ctx, src.NodeId)
	if err != nil {
		return nil, err
	}
	blob, err := d.net.Fetch(ctx, base, src.PieceRoot)
	if err != nil {
		return nil, err
	}
	if err := checkRoot(blob, src.PieceRoot); err != nil {
		return nil, fmt.Errorf("source replica: %w", err)
	}
	out, err := storagekey.Rewrap(seed, deal.DealNonce, src.Index, target.Index, blob)
	if err != nil {
		return nil, err
	}
	if err := checkRoot(out, target.PieceRoot); err != nil {
		return nil, ErrRootMismatch
	}
	return out, nil
}

func checkRoot(body, root []byte) error {
	c, err := piece.Commit(body)
	if err != nil {
		return err
	}
	if !bytes.Equal(c.Root, root) {
		return errors.New("piece root does not match")
	}
	return nil
}
