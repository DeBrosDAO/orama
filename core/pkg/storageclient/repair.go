package storageclient

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/storagefile"
)

// ErrNoSource is a Repair with slots to rebuild and no accepted replica to
// copy: the deal's first upload belongs to Put.
var ErrNoSource = errors.New("no accepted replica to rebuild from")

// Repaired is one slot Repair uploaded.
type Repaired struct {
	Slot     uint32
	From     uint32
	Provider string
}

// Repair is the client's own repair, for a deal that names no repair
// delegate. Every slot that is assigned but not accepted is rebuilt from an
// accepted replica fetched from another provider: the source's outer layer
// is stripped and the target slot's applied with the repair seed, the result
// is checked against the target's on-chain root, and then it is uploaded to
// the target's provider. The plaintext is never recovered. A repair seed
// that is not this deal's makes the result miss the root and uploads nothing.
func (c *Client) Repair(ctx context.Context, dealID uint64, repairSeed []byte) ([]Repaired, error) {
	deal, err := c.chain.Deal(ctx, dealID)
	if err != nil {
		return nil, err
	}
	var sources, targets []Slot
	for i := uint32(0); i < deal.Replicas; i++ {
		slot, err := c.chain.Slot(ctx, dealID, i)
		if err != nil {
			return nil, err
		}
		switch {
		case slot.Status == slotActive && slot.Accepted:
			sources = append(sources, slot)
		case slot.Status == slotAssigned && !slot.Accepted && slot.Held():
			targets = append(targets, slot)
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("deal %d: %w", dealID, ErrNoSource)
	}
	ctx, cancel := context.WithTimeout(ctx, c.wait)
	defer cancel()
	var done []Repaired
	var errs []error
	for _, target := range targets {
		r, err := c.repairSlot(ctx, deal, repairSeed, sources, target)
		if err != nil {
			errs = append(errs, fmt.Errorf("slot %d: %w", target.Index, err))
			continue
		}
		done = append(done, r)
	}
	return done, errors.Join(errs...)
}

func (c *Client) repairSlot(ctx context.Context, deal Deal, seed []byte, sources []Slot, target Slot) (Repaired, error) {
	dest, err := c.chain.ProviderURL(ctx, target.NodeID)
	if err != nil {
		return Repaired{}, err
	}
	var errs []error
	for _, src := range sources {
		body, err := c.rebuild(ctx, deal, seed, src, target)
		if errors.Is(err, ErrRootMismatch) || errors.Is(err, storagefile.ErrNotForKey) {
			return Repaired{}, err
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("from slot %d: %w", src.Index, err))
			continue
		}
		if err := c.upload(ctx, dest, target.PieceRoot, body); err != nil {
			return Repaired{}, fmt.Errorf("upload to %s: %w", target.NodeID, err)
		}
		return Repaired{Slot: target.Index, From: src.Index, Provider: target.NodeID}, nil
	}
	return Repaired{}, fmt.Errorf("%w: %w", ErrNoReplica, errors.Join(errs...))
}

func (c *Client) rebuild(ctx context.Context, deal Deal, seed []byte, src, target Slot) ([]byte, error) {
	base, err := c.chain.ProviderURL(ctx, src.NodeID)
	if err != nil {
		return nil, err
	}
	blob, err := Fetch(ctx, c.http, base, src.PieceRoot)
	if err != nil {
		return nil, err
	}
	out, err := storagefile.Rewrap(seed, deal.Nonce, src.Index, target.Index, blob)
	if err != nil {
		return nil, err
	}
	if err := checkRoot(out, target.PieceRoot); err != nil {
		return nil, fmt.Errorf("rewrapped replica for slot %d (is this deal's repair seed in use?): %w", target.Index, err)
	}
	return out, nil
}
